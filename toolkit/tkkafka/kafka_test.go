package tkkafka

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/confluentinc/confluent-kafka-go/v2/kafka"
)

type queuedDelivery struct {
	message *kafka.Message
	events  chan kafka.Event
}

type fakeProducer struct {
	mu             sync.Mutex
	events         chan kafka.Event
	records        []*kafka.Message
	queued         []queuedDelivery
	autoDelivery   bool
	deliverOnFlush bool
	produceErr     error
	deliveryErr    error
	closed         bool
	flushTimeout   int
}

func newFakeProducer() (producer *fakeProducer) {
	return &fakeProducer{events: make(chan kafka.Event, 8), autoDelivery: true}
}

func (p *fakeProducer) Produce(message *kafka.Message, delivery chan kafka.Event) (err error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.produceErr != nil {
		return p.produceErr
	}
	p.records = append(p.records, message)
	if p.autoDelivery {
		message.TopicPartition.Error = p.deliveryErr
		delivery <- message
		return
	}
	p.queued = append(p.queued, queuedDelivery{message: message, events: delivery})
	return
}

func (p *fakeProducer) Events() (events chan kafka.Event) {
	return p.events
}

func (p *fakeProducer) Flush(timeoutMs int) (remaining int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.flushTimeout = timeoutMs
	if p.deliverOnFlush {
		for _, queued := range p.queued {
			queued.message.TopicPartition.Error = p.deliveryErr
			queued.events <- queued.message
		}
		p.queued = nil
	}
	return len(p.queued)
}

func (p *fakeProducer) Close() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.closed = true
}

type fakeConsumer struct {
	mu           sync.Mutex
	events       chan kafka.Event
	commits      [][]kafka.TopicPartition
	commitSignal chan struct{}
	subscribed   []string
	subscribeErr error
	commitErr    error
	partitionErr error
	closed       bool
}

func newFakeConsumer(messages ...*kafka.Message) (consumer *fakeConsumer) {
	consumer = &fakeConsumer{events: make(chan kafka.Event, len(messages)+8), commitSignal: make(chan struct{}, 16)}
	for _, message := range messages {
		consumer.events <- message
	}
	return
}

func (c *fakeConsumer) SubscribeTopics(topics []string, _ kafka.RebalanceCb) (err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.subscribed = slices.Clone(topics)
	return c.subscribeErr
}

func (c *fakeConsumer) Poll(timeoutMs int) (event kafka.Event) {
	timer := time.NewTimer(time.Duration(timeoutMs) * time.Millisecond)
	defer timer.Stop()
	select {
	case event = <-c.events:
	case <-timer.C:
	}
	return
}

func (c *fakeConsumer) CommitOffsets(offsets []kafka.TopicPartition) (committed []kafka.TopicPartition, err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	committed = slices.Clone(offsets)
	c.commits = append(c.commits, committed)
	if c.partitionErr != nil {
		committed[0].Error = c.partitionErr
	}
	c.commitSignal <- struct{}{}
	return committed, c.commitErr
}

func (c *fakeConsumer) Close() (err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.closed = true
	return
}

func (c *fakeConsumer) committed() (commits [][]kafka.TopicPartition) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return slices.Clone(c.commits)
}

func record(partition int32, offset kafka.Offset) (message *kafka.Message) {
	return &kafka.Message{TopicPartition: kafka.TopicPartition{Topic: new("prod_orders"), Partition: partition, Offset: offset}, Value: []byte(`{"id":1}`)}
}

func testConfig() (cfg *Config) {
	return &Config{
		Env: "prod", ConsumerEnv: "worker", RetryMaxCount: 1, RetryDelay: time.Millisecond,
		TopicConfig: map[string]TopicConfig{"orders": {Mode: ModeBoth, PartitionNum: 1}},
	}
}

func fakeDrivers(producer *fakeProducer, consumer *fakeConsumer) (option Option) {
	return func(options *clientOptions) {
		options.pollInterval = time.Millisecond
		if producer != nil {
			options.producerFactory = func(_ *kafka.ConfigMap) (driver producerDriver, err error) { return producer, nil }
		}
		if consumer != nil {
			options.consumerFactory = func(_ *kafka.ConfigMap) (driver consumerDriver, err error) { return consumer, nil }
		}
	}
}

func mustClient(t *testing.T, cfg *Config, opts ...Option) (client *KafkaClient) {
	t.Helper()
	client, err := New(cfg, opts...)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		closeErr := client.Close()
		if closeErr != nil {
			t.Errorf("Close: %v", closeErr)
		}
	})
	return
}

func waitSubscription(t *testing.T, subscription *Subscription) (err error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	return subscription.Wait(ctx)
}

func waitCommit(t *testing.T, consumer *fakeConsumer) {
	t.Helper()
	select {
	case <-consumer.commitSignal:
	case <-time.After(time.Second):
		t.Fatal("commit did not occur")
	}
}

func TestConfigurationDefaultsAndOwnership(t *testing.T) {
	cfg := testConfig()
	cfg.TopicConfig["orders"] = TopicConfig{Mode: ModeBoth, Groups: []string{"audit"}}
	cfg.ExcludeTopics = []string{"hidden"}
	client := mustClient(t, cfg)
	cfg.TopicConfig["orders"].Groups[0] = "changed"
	cfg.ExcludeTopics[0] = "changed"
	delete(cfg.TopicConfig, "orders")
	snapshot := client.Config()
	if snapshot.TopicConfig["orders"].Groups[0] != "audit" || snapshot.ExcludeTopics[0] != "hidden" || snapshot.TopicConfig["orders"].PartitionNum != 12 {
		t.Fatalf("configuration retains caller-owned collections: %+v", snapshot)
	}
	snapshot.TopicConfig["orders"].Groups[0] = "snapshot"
	snapshot.ExcludeTopics[0] = "snapshot"
	if client.Config().TopicConfig["orders"].Groups[0] != "audit" || client.Config().ExcludeTopics[0] != "hidden" {
		t.Fatal("Config does not return an independent snapshot")
	}
	withConfig, err := NewWithConfig(nil)
	if err != nil {
		t.Fatal(err)
	}
	defer withConfig.Close()
	withOption, err := NewWithOption()
	if err != nil {
		t.Fatal(err)
	}
	defer withOption.Close()
	if !reflect.DeepEqual(withConfig.Config(), withOption.Config()) {
		t.Fatal("constructors disagree on defaults")
	}
	defaults := withConfig.Config()
	if defaults.WaitTimeout != -1 || defaults.RetryBackoff != 1000 || defaults.RetryDelay != 10*time.Second || defaults.BatchInterval != 5*time.Second || defaults.ConsumerEnv != "local" {
		t.Fatalf("unexpected defaults: %+v", defaults)
	}
}

func TestEncodersKeepInputAndNumberPrecision(t *testing.T) {
	input := map[string]any{"created_at": "today", "id": json.Number("9007199254740993"), "nested": map[string]any{"created_at": "keep"}}
	data, err := (JSONEncoder{}).Encode(input)
	if err != nil {
		t.Fatal(err)
	}
	var object map[string]json.RawMessage
	err = json.Unmarshal(data, &object)
	if err != nil {
		t.Fatal(err)
	}
	if _, exists := object["created_at"]; !exists {
		t.Fatal("default encoder removed a business field")
	}
	data, err = (LegacyEncoder{}).Encode(input)
	if err != nil {
		t.Fatal(err)
	}
	object = nil
	err = json.Unmarshal(data, &object)
	if err != nil {
		t.Fatal(err)
	}
	_, hasTime := object["created_at"]
	if hasTime || string(object["id"]) != "9007199254740993" || input["created_at"] != "today" || string(object["nested"]) != `{"created_at":"keep"}` {
		t.Fatalf("unexpected legacy encoding or input mutation: %s %+v", data, input)
	}
	_, err = (LegacyEncoder{}).Encode([]int{1})
	if err == nil {
		t.Fatal("legacy encoder accepted a non-object")
	}
}

func TestSendUsesSharedProducerAndReturnsDelivery(t *testing.T) {
	producer := newFakeProducer()
	cfg := testConfig()
	cfg.GlobalProducer = "shared"
	cfg.BufferMemory = 1025
	cfg.TopicConfig["orders"] = TopicConfig{Mode: ModeBoth, PartitionNum: 12}
	cfg.TopicConfig["audit"] = TopicConfig{Mode: ModeProducer, PartitionNum: 12}
	created := 0
	client := mustClient(t, cfg, func(options *clientOptions) {
		options.producerFactory = func(config *kafka.ConfigMap) (driver producerDriver, err error) {
			created++
			if (*config)["queue.buffering.max.kbytes"] != 2 || (*config)["batch.size"] != 200 {
				t.Errorf("unexpected producer configuration: %+v", *config)
			}
			return producer, nil
		}
	})
	result, err := client.SendSync(t.Context(), "orders", &ProducerMessage{Key: "test", Data: map[string]any{"created_at": "keep"}})
	if err != nil {
		t.Fatal(err)
	}
	if *result.Message.TopicPartition.Topic != "prod_orders" || result.Message.TopicPartition.Partition != int32(0xafd071e5%12) || string(result.Message.Value) != `{"created_at":"keep"}` {
		t.Fatalf("unexpected record: %+v", result.Message)
	}
	result, err = client.SendSync(t.Context(), "audit", &ProducerMessage{Data: "data"})
	if err != nil {
		t.Fatal(err)
	}
	if created != 1 || result.Message.TopicPartition.Partition != kafka.PartitionAny || *result.Message.TopicPartition.Topic != "prod_audit" {
		t.Fatalf("producer sharing or empty-key routing failed: created=%d record=%+v", created, result.Message)
	}
}

func TestSendDistinguishesQueueAndDeliveryErrors(t *testing.T) {
	producer := newFakeProducer()
	failure := errors.New("delivery failed")
	producer.deliveryErr = failure
	client := mustClient(t, testConfig(), fakeDrivers(producer, nil))
	delivery, err := client.Send(t.Context(), "orders", &ProducerMessage{Data: 1})
	if err != nil {
		t.Fatal(err)
	}
	result := <-delivery
	if !errors.Is(result.Err, failure) {
		t.Fatalf("delivery failure hidden: %+v", result)
	}
	producer.produceErr = errors.New("queue full")
	delivery, err = client.Send(t.Context(), "orders", &ProducerMessage{Data: 2})
	if !errors.Is(err, producer.produceErr) || delivery != nil {
		t.Fatalf("queue failure hidden: channel=%v err=%v", delivery, err)
	}
}

func TestSendCancellationDoesNotWithdrawQueuedMessage(t *testing.T) {
	producer := newFakeProducer()
	producer.autoDelivery = false
	producer.deliverOnFlush = true
	client := mustClient(t, testConfig(), fakeDrivers(producer, nil))
	ctx, cancel := context.WithCancel(t.Context())
	delivery, err := client.Send(ctx, "orders", &ProducerMessage{Data: 1})
	if err != nil {
		t.Fatal(err)
	}
	cancel()
	err = client.Close()
	if err != nil {
		t.Fatal(err)
	}
	result := <-delivery
	if result.Err != nil || !producer.closed || producer.flushTimeout <= 0 {
		t.Fatalf("queued record lost during cancellation/close: %+v", result)
	}
}

func TestSendSyncWaitCanBeCanceled(t *testing.T) {
	producer := newFakeProducer()
	producer.autoDelivery = false
	producer.deliverOnFlush = true
	client := mustClient(t, testConfig(), fakeDrivers(producer, nil))
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Millisecond)
	defer cancel()
	_, err := client.SendSync(ctx, "orders", &ProducerMessage{Data: 1})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("wait not bounded by context: %v", err)
	}
}

func TestUnconfirmedDeliveryIsVisibleAtClose(t *testing.T) {
	producer := newFakeProducer()
	producer.autoDelivery = false
	client, err := New(testConfig(), fakeDrivers(producer, nil))
	if err != nil {
		t.Fatal(err)
	}
	delivery, err := client.Send(t.Context(), "orders", &ProducerMessage{Data: 1})
	if err != nil {
		t.Fatal(err)
	}
	err = client.Close()
	if !errors.Is(err, ErrUndelivered) {
		t.Fatalf("Close did not report queued records: %v", err)
	}
	result := <-delivery
	if !errors.Is(result.Err, ErrUndelivered) || !producer.closed {
		t.Fatalf("delivery did not terminate: %+v", result)
	}
	_, err = client.Send(t.Context(), "orders", &ProducerMessage{Data: 2})
	if !errors.Is(err, ErrClosed) {
		t.Fatalf("send after close: %v", err)
	}
	for range client.Errors() {
	}
}

func TestRetryExhaustionStopsWithoutCommit(t *testing.T) {
	consumer := newFakeConsumer(record(0, 0), record(0, 1))
	cfg := testConfig()
	cfg.RetryMaxCount = 2
	client := mustClient(t, cfg, fakeDrivers(nil, consumer))
	failure := errors.New("business failed")
	var calls atomic.Int32
	subscription, err := client.SubscribeContext(t.Context(), "orders", func(context.Context, *kafka.Message) (handlerErr error) {
		calls.Add(1)
		return failure
	})
	if err != nil {
		t.Fatal(err)
	}
	err = waitSubscription(t, subscription)
	if !errors.Is(err, failure) || calls.Load() != 3 || len(consumer.committed()) != 0 || !consumer.closed {
		t.Fatalf("retry/commit mismatch: err=%v calls=%d commits=%v", err, calls.Load(), consumer.committed())
	}
}

func TestSuccessfulBatchCommitsEveryPartition(t *testing.T) {
	consumer := newFakeConsumer(record(0, 2), record(1, 6), record(0, 3), record(1, 9))
	cfg := testConfig()
	cfg.BatchSize = 4
	client := mustClient(t, cfg, fakeDrivers(nil, consumer))
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	subscription, err := client.BatchSubscribeContext(ctx, "orders", func(_ context.Context, messages []*kafka.Message) (handlerErr error) {
		if len(messages) != 4 {
			t.Errorf("batch size=%d", len(messages))
		}
		// 业务代码修改元数据也不能移动已经保存的确认位置。
		messages[0].TopicPartition.Offset = 999
		*messages[0].TopicPartition.Topic = "changed_by_handler"
		return
	})
	if err != nil {
		t.Fatal(err)
	}
	waitCommit(t, consumer)
	cancel()
	err = waitSubscription(t, subscription)
	if !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	commits := consumer.committed()
	if len(commits) != 1 || len(commits[0]) != 2 || *commits[0][0].Topic != "prod_orders" || commits[0][0].Partition != 0 || commits[0][0].Offset != 4 || commits[0][1].Partition != 1 || commits[0][1].Offset != 10 {
		t.Fatalf("incorrect partition progress: %+v", commits)
	}
}

func TestFailedBatchNeverAcknowledgesAnyPartition(t *testing.T) {
	consumer := newFakeConsumer(record(0, 2), record(1, 6))
	cfg := testConfig()
	cfg.BatchSize = 2
	client := mustClient(t, cfg, fakeDrivers(nil, consumer))
	failure := errors.New("batch failed")
	subscription, err := client.BatchSubscribeContext(t.Context(), "orders", func(context.Context, []*kafka.Message) (handlerErr error) { return failure })
	if err != nil {
		t.Fatal(err)
	}
	err = waitSubscription(t, subscription)
	if !errors.Is(err, failure) || len(consumer.committed()) != 0 {
		t.Fatalf("failed batch acknowledged: err=%v commits=%+v", err, consumer.committed())
	}
}

func TestSingleFailureDoesNotAdvancePastFailedRecord(t *testing.T) {
	consumer := newFakeConsumer(record(0, 0), record(1, 4), record(0, 2))
	client := mustClient(t, testConfig(), fakeDrivers(nil, consumer))
	failure := errors.New("partition one failed")
	subscription, err := client.SubscribeContext(t.Context(), "orders", func(_ context.Context, message *kafka.Message) (handlerErr error) {
		if message.TopicPartition.Partition == 1 {
			return failure
		}
		return
	})
	if err != nil {
		t.Fatal(err)
	}
	err = waitSubscription(t, subscription)
	commits := consumer.committed()
	if !errors.Is(err, failure) || len(commits) != 1 || commits[0][0].Partition != 0 || commits[0][0].Offset != 1 {
		t.Fatalf("advanced past a failed record: err=%v commits=%+v", err, commits)
	}
}

func TestRetryWaitAndPollObserveCancellation(t *testing.T) {
	for _, duringRetry := range []bool{false, true} {
		t.Run(map[bool]string{false: "idle_poll", true: "retry_timer"}[duringRetry], func(t *testing.T) {
			consumer := newFakeConsumer()
			if duringRetry {
				consumer.events <- record(0, 0)
			}
			cfg := testConfig()
			cfg.RetryMaxCount = 0
			cfg.RetryDelay = time.Hour
			cfg.WaitTimeout = -1
			client := mustClient(t, cfg, fakeDrivers(nil, consumer))
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			called := make(chan struct{}, 1)
			subscription, err := client.SubscribeContext(ctx, "orders", func(context.Context, *kafka.Message) (handlerErr error) {
				called <- struct{}{}
				return errors.New("retry me")
			})
			if err != nil {
				t.Fatal(err)
			}
			if duringRetry {
				select {
				case <-called:
				case <-time.After(time.Second):
					t.Fatal("handler did not start")
				}
			}
			cancel()
			err = waitSubscription(t, subscription)
			if !errors.Is(err, context.Canceled) || len(consumer.committed()) != 0 {
				t.Fatalf("cancellation not observed: err=%v commits=%v", err, consumer.committed())
			}
		})
	}
}

func TestBatchIntervalAndCommitFailure(t *testing.T) {
	consumer := newFakeConsumer(record(0, 8))
	failure := errors.New("commit failed")
	consumer.partitionErr = failure
	cfg := testConfig()
	cfg.BatchInterval = 5 * time.Millisecond
	client := mustClient(t, cfg, fakeDrivers(nil, consumer))
	subscription, err := client.BatchSubscribeContext(t.Context(), "orders", func(context.Context, []*kafka.Message) (handlerErr error) { return })
	if err != nil {
		t.Fatal(err)
	}
	err = waitSubscription(t, subscription)
	if !errors.Is(err, failure) || len(consumer.committed()) != 1 || consumer.committed()[0][0].Offset != 9 {
		t.Fatalf("interval flush or partition commit error hidden: %v", err)
	}
}

func TestCloseBoundsWaitingForUncooperativeHandler(t *testing.T) {
	consumer := newFakeConsumer(record(0, 0))
	client := mustClient(t, testConfig(), fakeDrivers(nil, consumer), WithCloseTimeout(20*time.Millisecond))
	entered := make(chan struct{})
	release := make(chan struct{})
	subscription, err := client.SubscribeContext(t.Context(), "orders", func(context.Context, *kafka.Message) (handlerErr error) {
		close(entered)
		<-release
		return
	})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("handler did not start")
	}
	err = client.Close()
	close(release)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("uncooperative handler blocked Close: %v", err)
	}
	err = waitSubscription(t, subscription)
	if !errors.Is(err, ErrClosed) || len(consumer.committed()) != 0 {
		t.Fatalf("close acknowledged an in-flight callback: %v %+v", err, consumer.committed())
	}
	err = client.Close()
	if err != nil {
		t.Fatal(err)
	}
}

func TestCloseBoundsWaitingForResourceCreation(t *testing.T) {
	producer := newFakeProducer()
	entered := make(chan struct{})
	release := make(chan struct{})
	client := mustClient(t, testConfig(), WithCloseTimeout(20*time.Millisecond), func(options *clientOptions) {
		options.producerFactory = func(_ *kafka.ConfigMap) (driver producerDriver, err error) {
			close(entered)
			<-release
			return producer, nil
		}
	})
	created := make(chan error, 1)
	go func() { created <- client.NewProducer(t.Context(), "orders") }()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("factory did not start")
	}
	err := client.Close()
	close(release)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("resource creation blocked Close: %v", err)
	}
	select {
	case err = <-created:
		if !errors.Is(err, ErrClosed) {
			t.Fatalf("resource published after Close: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("resource creation did not finish")
	}
	err = client.Close()
	if err != nil || !producer.closed {
		t.Fatalf("created resource not closed: %v", err)
	}
}

func TestConsumerGroupNamingAndSelection(t *testing.T) {
	cfg := testConfig()
	cfg.TopicConfig["orders"] = TopicConfig{Mode: ModeConsumer, PartitionNum: 1, Groups: []string{"audit", "billing"}}
	var names []string
	var consumers []*fakeConsumer
	client := mustClient(t, cfg, func(options *clientOptions) {
		options.pollInterval = time.Millisecond
		options.consumerFactory = func(config *kafka.ConfigMap) (driver consumerDriver, err error) {
			if (*config)["enable.auto.commit"] != false || (*config)["enable.auto.offset.store"] != false {
				t.Error("automatic acknowledgement enabled")
			}
			names = append(names, (*config)["group.id"].(string))
			consumer := newFakeConsumer()
			consumers = append(consumers, consumer)
			return consumer, nil
		}
	})
	_, err := client.SubscribeContext(t.Context(), "orders", func(context.Context, *kafka.Message) (handlerErr error) { return }, "unknown")
	if err == nil || len(consumers) != 0 {
		t.Fatal("invalid group created a consumer")
	}
	subscription, err := client.SubscribeContext(t.Context(), "orders", func(context.Context, *kafka.Message) (handlerErr error) { return })
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(names, []string{"worker_group_audit", "worker_group_billing"}) {
		t.Fatalf("unexpected group names: %+v", names)
	}
	for _, consumer := range consumers {
		if !slices.Equal(consumer.subscribed, []string{"prod_orders"}) {
			t.Fatalf("unexpected topic names: %v", consumer.subscribed)
		}
	}
	subscription.Stop()
	_ = waitSubscription(t, subscription)
	_, err = client.SubscribeContext(t.Context(), "orders", func(context.Context, *kafka.Message) (handlerErr error) { return })
	if !errors.Is(err, ErrAlreadySubscribed) {
		t.Fatalf("reused closed consumer: %v", err)
	}
}

func TestPartialConsumerCreationClosesAllocatedResources(t *testing.T) {
	cfg := testConfig()
	cfg.TopicConfig["orders"] = TopicConfig{Mode: ModeConsumer, PartitionNum: 2}
	consumer := newFakeConsumer()
	failure := errors.New("creation failed")
	created := 0
	client := mustClient(t, cfg, func(options *clientOptions) {
		options.consumerFactory = func(_ *kafka.ConfigMap) (driver consumerDriver, err error) {
			created++
			if created == 2 {
				return nil, failure
			}
			return consumer, nil
		}
	})
	err := client.NewConsumer(t.Context(), "orders")
	if !errors.Is(err, failure) || !consumer.closed || len(client.consumers) != 0 {
		t.Fatalf("partial construction leaked: err=%v closed=%v", err, consumer.closed)
	}
}

func TestDisabledExcludedAndModeAreExplicit(t *testing.T) {
	for _, scenario := range []struct {
		name      string
		configure func(cfg *Config)
		want      error
	}{
		{name: "disabled", configure: func(cfg *Config) { cfg.IsClose = true }, want: ErrDisabled},
		{name: "excluded", configure: func(cfg *Config) { cfg.ExcludeTopics = []string{"orders"} }, want: ErrExcluded},
		{name: "consumer_only", configure: func(cfg *Config) { cfg.TopicConfig["orders"] = TopicConfig{Mode: ModeConsumer} }, want: ErrDisabled},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			cfg := testConfig()
			scenario.configure(cfg)
			client := mustClient(t, cfg)
			_, err := client.Send(t.Context(), "orders", &ProducerMessage{Data: 1})
			if !errors.Is(err, scenario.want) || len(client.producers) != 0 {
				t.Fatalf("unexpected disabled behavior: %v", err)
			}
		})
	}
}
