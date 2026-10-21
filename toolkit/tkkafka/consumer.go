package tkkafka

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"github.com/confluentinc/confluent-kafka-go/v2/kafka"
)

type consumerKey struct {
	topic string
	group string
}

type consumerState struct {
	driver    consumerDriver
	started   bool
	closeOnce sync.Once
	closeErr  error
}

func (state *consumerState) close() (err error) {
	state.closeOnce.Do(func() { state.closeErr = state.driver.Close() })
	return state.closeErr
}

// Subscription 表示一个或多个消费组的消费任务，首次失败会取消同一订阅的其他消费者。
// 重试耗尽会结束订阅，不提交失败消息或批次；重新启动需使用新客户端。
type Subscription struct {
	done      chan struct{}
	ctx       context.Context
	cancel    context.CancelCauseFunc
	remaining atomic.Int32
	mu        sync.Mutex
	err       error
}

// Done 在全部消费者及其驱动完成关闭后关闭。
func (s *Subscription) Done() (done <-chan struct{}) {
	return s.done
}

// Stop 请求取消订阅，不强行终止不响应 ctx 的用户处理函数。
func (s *Subscription) Stop() {
	s.cancel(context.Canceled)
}

// Err 返回已记录的任务错误；Done 关闭后该结果保持不变。
func (s *Subscription) Err() (err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.err
}

// Wait 等待订阅退出；ctx 只限制调用方的等待，不取消订阅。
func (s *Subscription) Wait(ctx context.Context) (err error) {
	select {
	case <-s.done:
		return s.Err()
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *Subscription) fail(err error) {
	if err == nil {
		return
	}
	s.mu.Lock()
	if s.err == nil {
		s.err = err
	}
	s.mu.Unlock()
	s.cancel(err)
}

// NewConsumer 创建 topic 下各消费组的消费者，每组数量沿用 PartitionNum。
// 未配置 Groups 时使用 <ConsumerEnv>_group_<topic>，否则使用各组后缀。
func (c *KafkaClient) NewConsumer(ctx context.Context, topic string) (err error) {
	err = ctx.Err()
	if err != nil {
		return
	}
	cfg, err := c.topicConfig(topic)
	if err != nil {
		return
	}
	if c.config.IsClose || cfg.Mode != ModeConsumer && cfg.Mode != ModeBoth {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	_, err = c.ensureConsumersLocked(topic, consumerGroups(topic, cfg))
	return
}

func consumerGroups(topic string, cfg TopicConfig) (groups []string) {
	if len(cfg.Groups) == 0 {
		return []string{topic}
	}
	return cfg.Groups
}

func (c *KafkaClient) ensureConsumersLocked(topic string, groups []string) (states []*consumerState, err error) {
	if c.closed.Load() {
		err = ErrClosed
		return
	}
	created := make(map[consumerKey][]*consumerState)
	defer func() {
		if err != nil {
			for _, list := range created {
				for _, state := range list {
					err = errors.Join(err, state.close())
				}
			}
		}
	}()
	for _, group := range groups {
		key := consumerKey{topic: topic, group: group}
		existing := c.consumers[key]
		if existing != nil {
			states = append(states, existing...)
			continue
		}
		cfg := c.driverConfig()
		cfg["group.id"] = c.config.ConsumerEnv + "_group_" + group
		cfg["auto.offset.reset"] = c.config.OffsetReset
		cfg["heartbeat.interval.ms"] = 3000
		cfg["session.timeout.ms"] = 30000
		cfg["max.poll.interval.ms"] = 60000
		cfg["fetch.max.bytes"] = 52428800
		cfg["max.partition.fetch.bytes"] = 104857600
		cfg["enable.auto.commit"] = false
		cfg["enable.auto.offset.store"] = false
		for range c.config.TopicConfig[topic].PartitionNum {
			driver, createErr := c.options.consumerFactory(&cfg)
			if createErr != nil {
				err = createErr
				return
			}
			if driver == nil {
				err = fmt.Errorf("tkkafka: consumer factory returned nil")
				return
			}
			state := &consumerState{driver: driver}
			created[key] = append(created[key], state)
			states = append(states, state)
			if c.closed.Load() {
				err = ErrClosed
				return
			}
		}
	}
	for key, list := range created {
		c.consumers[key] = list
	}
	return
}

// Subscribe 兼容旧单条回调并在后台消费，异步错误进入 Errors。
// 需要可靠等待结果和可取消处理函数时使用 SubscribeContext。
func (c *KafkaClient) Subscribe(ctx context.Context, topic string, fn func(message *kafka.Message) error, group ...string) (err error) {
	if fn == nil {
		err = fmt.Errorf("tkkafka: nil consumer handler")
		return
	}
	_, err = c.SubscribeContext(ctx, topic, func(_ context.Context, message *kafka.Message) (handlerErr error) {
		return fn(message)
	}, group...)
	return
}

// BatchSubscribe 兼容旧批处理回调，批次失败时整批不确认。
func (c *KafkaClient) BatchSubscribe(ctx context.Context, topic string, fn func(messages []*kafka.Message) error, group ...string) (err error) {
	if fn == nil {
		err = fmt.Errorf("tkkafka: nil batch consumer handler")
		return
	}
	_, err = c.BatchSubscribeContext(ctx, topic, func(_ context.Context, messages []*kafka.Message) (handlerErr error) {
		return fn(messages)
	}, group...)
	return
}

// SubscribeContext 启动单条消费，handler 成功后才提交该 partition 的下一条 offset。
// 不指定 group 时订阅配置中的全部组；指定时只允许该 topic 已配置的组后缀。
func (c *KafkaClient) SubscribeContext(ctx context.Context, topic string, fn func(context.Context, *kafka.Message) error, group ...string) (subscription *Subscription, err error) {
	if fn == nil {
		err = fmt.Errorf("tkkafka: nil consumer handler")
		return
	}
	return c.subscribe(ctx, topic, false, func(ctx context.Context, messages []*kafka.Message) (handlerErr error) {
		return fn(ctx, messages[0])
	}, group)
}

// BatchSubscribeContext 按 BatchSize 或 BatchInterval 批处理消息。
// 成功后分别提交各 partition 已处理的进度；失败、取消或关闭时不确认未完成批次。
func (c *KafkaClient) BatchSubscribeContext(ctx context.Context, topic string, fn func(context.Context, []*kafka.Message) error, group ...string) (subscription *Subscription, err error) {
	if fn == nil {
		err = fmt.Errorf("tkkafka: nil batch consumer handler")
		return
	}
	return c.subscribe(ctx, topic, true, fn, group)
}

func (c *KafkaClient) subscribe(ctx context.Context, topic string, batch bool, handler func(context.Context, []*kafka.Message) error, selected []string) (subscription *Subscription, err error) {
	err = ctx.Err()
	if err != nil {
		return
	}
	cfg, err := c.topicConfig(topic)
	if err != nil {
		return
	}
	if c.config.IsClose || cfg.Mode != ModeConsumer && cfg.Mode != ModeBoth {
		err = ErrDisabled
		return
	}
	groups := consumerGroups(topic, cfg)
	if len(selected) > 0 {
		seen := make(map[string]bool, len(selected))
		for _, group := range selected {
			if !slices.Contains(groups, group) || seen[group] {
				err = fmt.Errorf("tkkafka: invalid or duplicate group %q for topic %q", group, topic)
				return
			}
			seen[group] = true
		}
		groups = selected
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	states, err := c.ensureConsumersLocked(topic, groups)
	if err != nil {
		return
	}
	for _, state := range states {
		if state.started {
			err = ErrAlreadySubscribed
			return
		}
	}
	for _, state := range states {
		err = state.driver.SubscribeTopics([]string{c.fullTopic(topic)}, nil)
		if err != nil {
			// 启动尚未发布，关闭本次涉及的全部资源，允许重新创建。
			for _, selectedState := range states {
				err = errors.Join(err, selectedState.close())
			}
			for _, group := range groups {
				delete(c.consumers, consumerKey{topic: topic, group: group})
			}
			return
		}
	}
	subCtx, cancel := context.WithCancelCause(ctx)
	subscription = &Subscription{done: make(chan struct{}), ctx: subCtx, cancel: cancel}
	subscription.remaining.Store(int32(len(states)))
	stopClientCancel := context.AfterFunc(c.ctx, func() { cancel(ErrClosed) })
	for _, state := range states {
		state.started = true
		sub := subscription
		c.wg.Go(func() {
			consumeErr := c.consume(sub.ctx, state.driver, batch, handler)
			sub.fail(consumeErr)
			closeErr := state.close()
			c.addCloseError(closeErr)
			sub.fail(closeErr)
			if sub.remaining.Add(-1) == 0 {
				stopClientCancel()
				sub.cancel(context.Canceled)
				c.report(sub.Err())
				close(sub.done)
			}
		})
	}
	return
}

func (c *KafkaClient) consume(ctx context.Context, consumer consumerDriver, batch bool, handler func(context.Context, []*kafka.Message) error) (err error) {
	size := 1
	if batch {
		size = c.config.BatchSize
	}
	messages := make([]*kafka.Message, 0, size)
	var batchStart time.Time
	for {
		err = context.Cause(ctx)
		if err != nil {
			return
		}
		if len(messages) > 0 && time.Since(batchStart) >= c.config.BatchInterval {
			err = c.handleAndCommit(ctx, consumer, messages, handler)
			if err != nil {
				return
			}
			messages = make([]*kafka.Message, 0, size)
		}
		interval := c.options.pollInterval
		if c.config.WaitTimeout > 0 {
			interval = min(interval, c.config.WaitTimeout)
		}
		if len(messages) > 0 {
			interval = min(interval, max(time.Nanosecond, c.config.BatchInterval-time.Since(batchStart)))
		}
		event := consumer.Poll(durationMilliseconds(interval))
		switch value := event.(type) {
		case *kafka.Message:
			if value == nil || value.TopicPartition.Topic == nil || value.TopicPartition.Offset < 0 {
				err = fmt.Errorf("tkkafka: invalid consumed message")
				return
			}
			if value.TopicPartition.Error != nil {
				err = value.TopicPartition.Error
				return
			}
			if len(messages) == 0 {
				batchStart = time.Now()
			}
			messages = append(messages, value)
			if len(messages) >= size {
				err = c.handleAndCommit(ctx, consumer, messages, handler)
				if err != nil {
					return
				}
				messages = make([]*kafka.Message, 0, size)
			}
		case kafka.Error:
			if value.Code() == kafka.ErrTimedOut {
				continue
			}
			if value.IsFatal() {
				err = value
				return
			}
			c.report(value)
		}
	}
}

func (c *KafkaClient) handleAndCommit(ctx context.Context, consumer consumerDriver, messages []*kafka.Message, handler func(context.Context, []*kafka.Message) error) (err error) {
	// 在调用业务代码前保存确认位置，避免回调修改消息元数据后改变提交进度。
	offsets := successfulOffsets(messages)
	err = c.retryHandler(ctx, func() (handlerErr error) { return handler(ctx, messages) })
	if err != nil {
		return
	}
	// 回调执行中发生取消时，不确认本次处理结果。
	err = context.Cause(ctx)
	if err != nil {
		return
	}
	// 同一消费者串行处理，每个分区此前读到的记录均已成功，允许跳过 Kafka 自身的稀疏 offset。
	committed, err := consumer.CommitOffsets(offsets)
	if err != nil {
		err = fmt.Errorf("tkkafka: commit offsets: %w", err)
		return
	}
	for _, partition := range committed {
		if partition.Error != nil {
			err = errors.Join(err, fmt.Errorf("tkkafka: commit partition %d: %w", partition.Partition, partition.Error))
		}
	}
	return
}

func (c *KafkaClient) retryHandler(ctx context.Context, handler func() error) (err error) {
	for attempt := 0; ; attempt++ {
		err = context.Cause(ctx)
		if err != nil {
			return
		}
		err = invokeHandler(handler)
		if err == nil {
			return
		}
		if c.config.RetryMaxCount > 0 && attempt >= c.config.RetryMaxCount {
			err = fmt.Errorf("tkkafka: handler failed after %d retries: %w", attempt, err)
			return
		}
		timer := time.NewTimer(c.config.RetryDelay)
		select {
		case <-ctx.Done():
			timer.Stop()
			err = context.Cause(ctx)
			return
		case <-timer.C:
		}
	}
}

func invokeHandler(handler func() error) (err error) {
	defer func() {
		panicValue := recover()
		if panicValue != nil {
			err = fmt.Errorf("tkkafka: consumer handler panic: %v", panicValue)
		}
	}()
	return handler()
}

func successfulOffsets(messages []*kafka.Message) (offsets []kafka.TopicPartition) {
	type partitionKey struct {
		topic     string
		partition int32
	}
	progress := make(map[partitionKey]kafka.TopicPartition)
	for _, message := range messages {
		tp := kafka.TopicPartition{
			Topic:     new(*message.TopicPartition.Topic),
			Partition: message.TopicPartition.Partition,
			Offset:    message.TopicPartition.Offset + 1,
		}
		key := partitionKey{topic: *tp.Topic, partition: tp.Partition}
		previous, exists := progress[key]
		if !exists || tp.Offset > previous.Offset {
			tp.Error = nil
			progress[key] = tp
		}
	}
	offsets = make([]kafka.TopicPartition, 0, len(progress))
	for _, partition := range progress {
		offsets = append(offsets, partition)
	}
	slices.SortFunc(offsets, func(a, b kafka.TopicPartition) (order int) {
		return cmp.Or(cmp.Compare(*a.Topic, *b.Topic), cmp.Compare(a.Partition, b.Partition))
	})
	return
}
