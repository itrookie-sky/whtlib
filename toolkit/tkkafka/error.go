package tkkafka

import (
	"errors"
	"fmt"
)

var (
	// ErrClosed 表示客户端已关闭。
	ErrClosed = errors.New("tkkafka: client closed")
	// ErrDisabled 表示客户端或 topic 的对应能力未启用。
	ErrDisabled = errors.New("tkkafka: client or topic mode disabled")
	// ErrExcluded 表示 topic 被排除生产。
	ErrExcluded = errors.New("tkkafka: topic excluded from production")
	// ErrAlreadySubscribed 表示消费者已经订阅。
	ErrAlreadySubscribed = errors.New("tkkafka: consumer already subscribed")
	// ErrUndelivered 表示关闭时消息仍未获得投递确认。
	ErrUndelivered = errors.New("tkkafka: delivery incomplete at shutdown")
	// ErrInvalidConfig 表示配置或注入依赖无效。
	ErrInvalidConfig = errors.New("tkkafka: invalid config")
	// ErrInvalidArgument 表示消息、handler 或消费组参数无效。
	ErrInvalidArgument = errors.New("tkkafka: invalid argument")
	// ErrTopicNotConfigured 表示请求的 topic 尚未配置。
	ErrTopicNotConfigured = errors.New("tkkafka: topic not configured")
	// ErrEncode 表示生产消息编码失败。
	ErrEncode = errors.New("tkkafka: encode failed")
	// ErrDelivery 表示排队或最终投递失败。
	ErrDelivery = errors.New("tkkafka: delivery failed")
	// ErrCommit 表示消费位点提交失败。
	ErrCommit = errors.New("tkkafka: commit offsets")
	// ErrHandler 表示消费回调执行失败。
	ErrHandler = errors.New("tkkafka: handler failed")
	// ErrHandlerPanic 表示消费回调发生 panic，同时归属 ErrHandler。
	ErrHandlerPanic = errors.New("tkkafka: consumer handler panic")
)

func invalidConfig(format string, args ...any) (err error) {
	return fmt.Errorf("%w: %s", ErrInvalidConfig, fmt.Sprintf(format, args...))
}

func invalidArgument(format string, args ...any) (err error) {
	return fmt.Errorf("%w: %s", ErrInvalidArgument, fmt.Sprintf(format, args...))
}

func topicNotConfigured(topic string) (err error) {
	return fmt.Errorf("%w: %q", ErrTopicNotConfigured, topic)
}

func operationError(kind, cause error) (err error) {
	if cause == nil || errors.Is(cause, kind) {
		return cause
	}
	return fmt.Errorf("%w: %w", kind, cause)
}

func undeliveredMessages(remaining int) (err error) {
	return fmt.Errorf("%w: %d queued messages", ErrUndelivered, remaining)
}

func unexpectedDelivery(event any) (err error) {
	return fmt.Errorf("%w: unexpected delivery event %T", ErrDelivery, event)
}

func commitPartitionError(partition int32, cause error) (err error) {
	return fmt.Errorf("%w: partition %d: %w", ErrCommit, partition, cause)
}

func handlerFailure(attempt int, cause error) (err error) {
	return fmt.Errorf("%w after %d retries: %w", ErrHandler, attempt, cause)
}

func handlerPanic(value any) (err error) {
	cause, ok := value.(error)
	if ok {
		return errors.Join(ErrHandler, fmt.Errorf("%w: %w", ErrHandlerPanic, cause))
	}
	return errors.Join(ErrHandler, fmt.Errorf("%w: %v", ErrHandlerPanic, value))
}

func legacyEncodingError(cause error) (err error) {
	return fmt.Errorf("%w: legacy encoder requires a JSON object: %w", ErrEncode, cause)
}
