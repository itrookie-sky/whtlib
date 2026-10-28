package tkkafka

// ProducerConsumerStartMode 定义生产者和消费者的启动模式。
type ProducerConsumerStartMode int

const (
	ModeNone     ProducerConsumerStartMode = iota // 不启动生产者或消费者。
	ModeProducer                                  // 仅启动生产者。
	ModeConsumer                                  // 仅启动消费者。
	ModeBoth                                      // 同时启动生产者和消费者。
)
