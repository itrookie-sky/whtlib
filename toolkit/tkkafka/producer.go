package tkkafka

import (
	"bytes"
	"context"
	"fmt"
	"hash/fnv"
	"slices"

	"github.com/confluentinc/confluent-kafka-go/v2/kafka"
)

// DeliveryResult 表示驱动最终投递确认；Err 非 nil 表示投递失败或关闭前未完成确认。
// 成功确认的持久化强度仍取决于生产者 acks 和 broker 配置，本包沿用 acks=1。
type DeliveryResult struct {
	Message *kafka.Message
	Err     error
}

// NewProducer 按需创建生产者；设置 GlobalProducer 时多个 topic 共享同一个实例。
func (c *KafkaClient) NewProducer(ctx context.Context, topic string) (err error) {
	err = ctx.Err()
	if err != nil {
		return
	}
	cfg, err := c.topicConfig(topic)
	if err != nil {
		return
	}
	if cfg.Mode != ModeProducer && cfg.Mode != ModeBoth || c.config.IsClose {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	_, err = c.ensureProducerLocked(topic)
	return
}

func (c *KafkaClient) ensureProducerLocked(topic string) (producer producerDriver, err error) {
	if c.closed.Load() {
		err = ErrClosed
		return
	}
	name := c.producerName(topic)
	producer = c.producers[name]
	if producer != nil {
		return
	}
	cfg := c.driverConfig()
	cfg["message.max.bytes"] = c.config.MessageMaxBytes
	cfg["compression.type"] = c.config.CompressionType
	cfg["max.in.flight.requests.per.connection"] = 1
	cfg["sticky.partitioning.linger.ms"] = 1000
	cfg["batch.size"] = c.config.BatchSize
	cfg["linger.ms"] = c.config.LingerMs
	cfg["retries"] = c.config.Retries
	cfg["retry.backoff.ms"] = c.config.RetryBackoff
	cfg["acks"] = "1"
	if c.config.BufferMemory > 0 {
		kilobytes := c.config.BufferMemory / 1024
		if c.config.BufferMemory%1024 != 0 {
			kilobytes++
		}
		cfg["queue.buffering.max.kbytes"] = kilobytes
	}
	producer, err = c.options.producerFactory(&cfg)
	if err != nil {
		return
	}
	if producer == nil {
		err = fmt.Errorf("tkkafka: producer factory returned nil")
		return
	}
	if c.closed.Load() {
		producer.Close()
		producer = nil
		err = ErrClosed
		return
	}
	c.producers[name] = producer
	c.wg.Go(func() { c.producerEvents(producer) })
	return
}

func (c *KafkaClient) producerEvents(producer producerDriver) {
	for {
		select {
		case <-c.deliveryStop:
			return
		case event, ok := <-producer.Events():
			if !ok {
				return
			}
			kafkaErr, ok := event.(kafka.Error)
			if ok {
				c.report(kafkaErr)
			}
		}
	}
}

// Send 将消息加入生产队列并返回单条最终投递结果的缓冲通道。
// err 只表示编码或排队失败。排队后 ctx 取消不会撤回消息，结果通道仍会给出最终确认。
// 调用方不必接收通道来释放资源；关闭前没有收到确认时结果为 ErrUndelivered。
func (c *KafkaClient) Send(ctx context.Context, topic string, message *ProducerMessage) (delivery <-chan DeliveryResult, err error) {
	err = ctx.Err()
	if err != nil {
		return
	}
	if message == nil {
		err = fmt.Errorf("tkkafka: nil producer message")
		return
	}
	cfg, err := c.topicConfig(topic)
	if err != nil {
		return
	}
	if c.config.IsClose || cfg.Mode != ModeProducer && cfg.Mode != ModeBoth {
		err = ErrDisabled
		return
	}
	if slices.Contains(c.config.ExcludeTopics, topic) {
		err = ErrExcluded
		return
	}
	data, err := c.options.encoder.Encode(message.Data)
	if err != nil {
		return
	}
	record := &kafka.Message{
		TopicPartition: kafka.TopicPartition{Topic: new(c.fullTopic(topic)), Partition: partitionFor(message.Key, cfg.PartitionNum)},
		Key:            []byte(message.Key),
		Value:          bytes.Clone(data),
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	err = ctx.Err()
	if err != nil {
		return
	}
	producer, err := c.ensureProducerLocked(topic)
	if err != nil {
		return
	}
	events := make(chan kafka.Event, 1)
	err = producer.Produce(record, events)
	if err != nil {
		return
	}
	results := make(chan DeliveryResult, 1)
	delivery = results
	c.wg.Go(func() {
		defer close(results)
		result := c.awaitDelivery(events)
		results <- result
		if result.Err != nil {
			c.report(result.Err)
		}
	})
	return
}

func (c *KafkaClient) awaitDelivery(events <-chan kafka.Event) (result DeliveryResult) {
	// 关闭与确认同时就绪时优先保留已收到的最终投递结果。
	select {
	case event := <-events:
		return deliveryResult(event)
	default:
	}
	select {
	case event := <-events:
		return deliveryResult(event)
	case <-c.deliveryStop:
		select {
		case event := <-events:
			return deliveryResult(event)
		default:
			result.Err = ErrUndelivered
			return
		}
	}
}

func deliveryResult(event kafka.Event) (result DeliveryResult) {
	message, ok := event.(*kafka.Message)
	if !ok || message == nil {
		result.Err = fmt.Errorf("tkkafka: unexpected delivery event %T", event)
		return
	}
	result.Message = message
	result.Err = message.TopicPartition.Error
	return
}

// SendMessage 兼容旧发送入口，只确认成功排队；最终错误可从 Errors 观察。
// 需要单条可靠确认时应使用 Send 或 SendSync。
func (c *KafkaClient) SendMessage(ctx context.Context, topic string, message *ProducerMessage) (err error) {
	_, err = c.Send(ctx, topic, message)
	return
}

// SendSync 等待单条最终投递确认，ctx 只限制等待，不撤回已排队消息。
func (c *KafkaClient) SendSync(ctx context.Context, topic string, message *ProducerMessage) (result DeliveryResult, err error) {
	delivery, err := c.Send(ctx, topic, message)
	if err != nil {
		return
	}
	select {
	case result = <-delivery:
		err = result.Err
	case <-ctx.Done():
		err = ctx.Err()
	}
	return
}

func partitionFor(key string, partitionNum uint32) (partition int32) {
	if key == "" {
		return kafka.PartitionAny
	}
	hash := fnv.New32a()
	_, _ = hash.Write([]byte(key))
	return int32(hash.Sum32() % partitionNum)
}
