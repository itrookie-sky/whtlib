package tkkafka

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/itrookie-sky/whtlib/adapter"

	"github.com/confluentinc/confluent-kafka-go/v2/kafka"
)

// producerDriver 和 consumerDriver 将驱动操作隔离，便于无需 broker 的模块验证。
type producerDriver interface {
	Produce(message *kafka.Message, delivery chan kafka.Event) (err error)
	Events() (events chan kafka.Event)
	Flush(timeoutMs int) (remaining int)
	Close()
}

type consumerDriver interface {
	SubscribeTopics(topics []string, callback kafka.RebalanceCb) (err error)
	Poll(timeoutMs int) (event kafka.Event)
	CommitOffsets(offsets []kafka.TopicPartition) (committed []kafka.TopicPartition, err error)
	Close() (err error)
}

type clientOptions struct {
	encoder         Encoder
	logger          adapter.Logger
	closeTimeout    time.Duration
	pollInterval    time.Duration
	producerFactory func(cfg *kafka.ConfigMap) (producer producerDriver, err error)
	consumerFactory func(cfg *kafka.ConfigMap) (consumer consumerDriver, err error)
}

// Option 配置编码器、诊断日志和关闭等待策略。
type Option func(options *clientOptions)

// WithEncoder 注入显式的消息编码协议。
func WithEncoder(encoder Encoder) (option Option) {
	return func(options *clientOptions) { options.encoder = encoder }
}

// WithLogger 注入可选日志；日志不包含消息正文、键或认证信息。
func WithLogger(logger adapter.Logger) (option Option) {
	return func(options *clientOptions) { options.logger = logger }
}

// WithCloseTimeout 设置 Close 的最大等待时间及生产者 flush 总预算。
func WithCloseTimeout(timeout time.Duration) (option Option) {
	return func(options *clientOptions) { options.closeTimeout = timeout }
}

// WithPollInterval 设置轮询检查取消的间隔，默认 100ms。
func WithPollInterval(interval time.Duration) (option Option) {
	return func(options *clientOptions) { options.pollInterval = interval }
}

// KafkaClient 管理自有生产者、消费者及后台任务。构造时不连接 Kafka。
// 实例配置在构造后不可变，公开方法可并发调用。
type KafkaClient struct {
	config       Config
	options      clientOptions
	mu           sync.Mutex
	producers    map[string]producerDriver
	consumers    map[consumerKey][]*consumerState
	closed       atomic.Bool
	ctx          context.Context
	cancel       context.CancelFunc
	deliveryStop chan struct{}
	closeDone    chan struct{}
	closeErr     error
	errors       chan error
	wg           sync.WaitGroup
}

// Client 是 KafkaClient 的简短名称。
type Client = KafkaClient

// New 复制配置并统一填充默认值，生产和消费资源按需创建。
func New(cfg *Config, opts ...Option) (client *KafkaClient, err error) {
	config, err := normalizeConfig(cfg)
	if err != nil {
		return
	}
	options := clientOptions{
		encoder:      JSONEncoder{},
		closeTimeout: 10 * time.Second,
		pollInterval: 100 * time.Millisecond,
		producerFactory: func(cfg *kafka.ConfigMap) (producer producerDriver, err error) {
			return kafka.NewProducer(cfg)
		},
		consumerFactory: func(cfg *kafka.ConfigMap) (consumer consumerDriver, err error) {
			return kafka.NewConsumer(cfg)
		},
	}
	for _, opt := range opts {
		if opt != nil {
			opt(&options)
		}
	}
	if options.encoder == nil || options.closeTimeout <= 0 || options.pollInterval <= 0 {
		err = invalidConfig("encoder and positive lifecycle intervals are required")
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	client = &KafkaClient{
		config:       config,
		options:      options,
		producers:    make(map[string]producerDriver),
		consumers:    make(map[consumerKey][]*consumerState),
		ctx:          ctx,
		cancel:       cancel,
		deliveryStop: make(chan struct{}),
		closeDone:    make(chan struct{}),
		errors:       make(chan error, 128),
	}
	return
}

// NewWithConfig 兼容旧配置构造入口，默认值与 New 完全一致。
func NewWithConfig(cfg *Config, opts ...Option) (client *KafkaClient, err error) {
	return New(cfg, opts...)
}

// NewWithOption 兼容旧函数式配置入口，不保留调用方的 map 或 slice。
func NewWithOption(opts ...ConfigOption) (client *KafkaClient, err error) {
	cfg := &Config{TopicConfig: make(map[string]TopicConfig)}
	for _, opt := range opts {
		if opt != nil {
			opt(cfg)
		}
	}
	return New(cfg)
}

// Config 返回独立的配置快照，调用方修改快照不会影响客户端。
func (c *KafkaClient) Config() (cfg Config) {
	return cloneConfig(c.config)
}

// Errors 返回非阻塞诊断错误流，缓冲满时会丢弃新诊断。
// 需要可靠结果时应使用 Send/SendSync 的投递结果和 Subscription.Wait。
func (c *KafkaClient) Errors() (stream <-chan error) {
	return c.errors
}

// PrintClientConfig 仅打印不含认证和业务数据的配置摘要。
func (c *KafkaClient) PrintClientConfig(ctx context.Context) {
	if c.options.logger != nil {
		c.options.logger.Debugf(ctx, "kafka config: env=%s consumerEnv=%s topics=%d disabled=%t", c.config.Env, c.config.ConsumerEnv, len(c.config.TopicConfig), c.config.IsClose)
	}
}

func (c *KafkaClient) report(err error) {
	if err == nil {
		return
	}
	if c.options.logger != nil {
		c.options.logger.Errorf(context.Background(), "kafka: %v", err)
	}
	select {
	case c.errors <- err:
	default:
	}
}

func (c *KafkaClient) addCloseError(err error) {
	if err == nil {
		return
	}
	c.mu.Lock()
	c.closeErr = errors.Join(c.closeErr, err)
	c.mu.Unlock()
}

func (c *KafkaClient) topicConfig(topic string) (cfg TopicConfig, err error) {
	cfg, ok := c.config.TopicConfig[topic]
	if !ok {
		err = topicNotConfigured(topic)
	}
	return
}

func (c *KafkaClient) fullTopic(topic string) (name string) {
	return c.config.Env + "_" + topic
}

func (c *KafkaClient) producerName(topic string) (name string) {
	global := strings.Join(strings.Fields(c.config.GlobalProducer), "")
	if global != "" {
		return "producer_" + global
	}
	return "producer_" + topic
}

func (c *KafkaClient) driverConfig() (cfg kafka.ConfigMap) {
	cfg = kafka.ConfigMap{
		"bootstrap.servers": c.config.Servers,
		"security.protocol": c.config.Protocol,
	}
	if c.config.Mechanism != "" {
		cfg["sasl.mechanism"] = c.config.Mechanism
	}
	if c.config.UserName != "" || c.config.Password != "" {
		cfg["sasl.username"] = c.config.UserName
		cfg["sasl.password"] = c.config.Password
	}
	return
}

// Close 停止订阅、限时 flush 并等待后台任务结束，可传入额外的等待 ctx。
// 默认最多等待 WithCloseTimeout；超时后清理继续进行，重复 Close 可再次等待。
// 用户回调或驱动调用若不返回，无法强行终止；此时返回 context.DeadlineExceeded。
func (c *KafkaClient) Close(contexts ...context.Context) (err error) {
	if c.closed.CompareAndSwap(false, true) {
		c.cancel()
		go c.shutdown()
	}
	select {
	case <-c.closeDone:
		c.mu.Lock()
		err = c.closeErr
		c.mu.Unlock()
		return
	default:
	}
	parent := context.Background()
	if len(contexts) > 0 && contexts[0] != nil {
		parent = contexts[0]
	}
	ctx, cancel := context.WithTimeout(parent, c.options.closeTimeout)
	defer cancel()
	select {
	case <-c.closeDone:
		c.mu.Lock()
		err = c.closeErr
		c.mu.Unlock()
	case <-ctx.Done():
		err = ctx.Err()
	}
	return
}

func (c *KafkaClient) shutdown() {
	// 关闭标记阻止新资源和任务加入，锁外执行可能阻塞的驱动操作。
	c.mu.Lock()
	producers := make([]producerDriver, 0, len(c.producers))
	for _, producer := range c.producers {
		producers = append(producers, producer)
	}
	idle := make([]*consumerState, 0)
	for _, states := range c.consumers {
		for _, state := range states {
			if !state.started {
				idle = append(idle, state)
			}
		}
	}
	c.mu.Unlock()
	deadline := time.Now().Add(c.options.closeTimeout)
	for _, producer := range producers {
		remaining := producer.Flush(durationMilliseconds(max(0, time.Until(deadline))))
		if remaining > 0 {
			c.addCloseError(undeliveredMessages(remaining))
		}
		producer.Close()
	}
	close(c.deliveryStop)
	for _, state := range idle {
		c.addCloseError(state.close())
	}
	c.wg.Wait()
	close(c.errors)
	close(c.closeDone)
}

func durationMilliseconds(duration time.Duration) (milliseconds int) {
	if duration <= 0 {
		return 0
	}
	// 向上取整，避免亚毫秒正间隔变成忙轮询。
	milliseconds = int(duration / time.Millisecond)
	if duration%time.Millisecond != 0 {
		milliseconds++
	}
	return min(milliseconds, 2147483647)
}
