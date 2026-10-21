// Package tkkafka 提供不依赖业务框架的 Kafka 生产、消费和资源管理能力。
package tkkafka

import (
	"cmp"
	"fmt"
	"maps"
	"math"
	"slices"
	"time"
)

// ProducerConsumerStartMode 定义生产者和消费者的启动模式。
type ProducerConsumerStartMode int

const (
	ModeNone     ProducerConsumerStartMode = iota // 不启动生产者或消费者。
	ModeProducer                                  // 仅启动生产者。
	ModeConsumer                                  // 仅启动消费者。
	ModeBoth                                      // 同时启动生产者和消费者。
)

// TopicConfig 定义 topic 的分区数、启动模式及消费组后缀。
type TopicConfig struct {
	PartitionNum uint32                    `json:"partitionNum"`
	Mode         ProducerConsumerStartMode `json:"mode"`
	Groups       []string                  `json:"groups"`
}

// ProducerMessage 定义消息键及交给 Encoder 编码的数据。
type ProducerMessage struct {
	Key  string `json:"key"`
	Data any    `json:"data"`
}

// Config 保留原 plkafka 配置字段的类型、JSON 名称及单位。
// RetryBackoff、LingerMs 的单位为毫秒，BufferMemory 的单位为字节。
// BatchSize 同时映射到生产端 batch.size 字节数和消费端批次消息数量。
type Config struct {
	Servers         string                 `json:"servers"`
	Protocol        string                 `json:"protocol"`
	Mechanism       string                 `json:"mechanism"`
	UserName        string                 `json:"userName"`
	Password        string                 `json:"password"`
	Retries         int                    `json:"retries"`
	RetryBackoff    int                    `json:"retryBackoff"`
	WaitTimeout     time.Duration          `json:"waitTimeout"`
	RetryDelay      time.Duration          `json:"retryDelay"`
	RetryMaxCount   int                    `json:"retryMaxCount"`
	OffsetReset     string                 `json:"offsetReset"`
	BatchSize       int                    `json:"batchSize"`
	LingerMs        int                    `json:"lingerMs"`
	CompressionType string                 `json:"compressionType"`
	MessageMaxBytes int                    `json:"messageMaxBytes"`
	BatchInterval   time.Duration          `json:"batchInterval"`
	IsClose         bool                   `json:"isClose"`
	Env             string                 `json:"env"`
	ConsumerEnv     string                 `json:"consumerEnv"`
	GlobalProducer  string                 `json:"globalProducer"`
	TopicConfig     map[string]TopicConfig `json:"topicConfig"`
	ExcludeTopics   []string               `json:"excludeTopics"`
	BufferMemory    int                    `json:"bufferMemory"`
}

// ConfigOption 提供兼容原构造方式的配置修改入口。
type ConfigOption func(c *Config)

func cloneConfig(cfg Config) (out Config) {
	out = cfg
	out.TopicConfig = maps.Clone(cfg.TopicConfig)
	if out.TopicConfig == nil {
		out.TopicConfig = make(map[string]TopicConfig)
	}
	for topic, topicCfg := range out.TopicConfig {
		topicCfg.Groups = slices.Clone(topicCfg.Groups)
		out.TopicConfig[topic] = topicCfg
	}
	out.ExcludeTopics = slices.Clone(cfg.ExcludeTopics)
	return
}

func normalizeConfig(cfg *Config) (out Config, err error) {
	if cfg != nil {
		out = cloneConfig(*cfg)
	}
	if out.TopicConfig == nil {
		out.TopicConfig = make(map[string]TopicConfig)
	}
	out.Servers = cmp.Or(out.Servers, "127.0.0.1:9092")
	out.Protocol = cmp.Or(out.Protocol, "PLAINTEXT")
	out.OffsetReset = cmp.Or(out.OffsetReset, "earliest")
	out.Env = cmp.Or(out.Env, "local")
	out.ConsumerEnv = cmp.Or(out.ConsumerEnv, out.Env)
	out.CompressionType = cmp.Or(out.CompressionType, "gzip")
	if out.Retries <= 0 {
		out.Retries = math.MaxInt32
	}
	if out.RetryBackoff <= 0 {
		out.RetryBackoff = 1000
	}
	if out.WaitTimeout <= 0 {
		out.WaitTimeout = -1
	}
	out.RetryDelay = cmp.Or(out.RetryDelay, 10*time.Second)
	out.BatchInterval = cmp.Or(out.BatchInterval, 5*time.Second)
	out.BatchSize = cmp.Or(out.BatchSize, 200)
	if out.LingerMs <= 0 {
		out.LingerMs = 100
	}
	if out.MessageMaxBytes <= 0 {
		out.MessageMaxBytes = 16384
	}
	if out.RetryDelay < 0 || out.RetryMaxCount < 0 || out.BatchInterval < 0 || out.BatchSize < 0 || out.BufferMemory < 0 {
		err = fmt.Errorf("tkkafka: invalid negative retry, batch or buffer configuration")
		return
	}
	for topic, topicCfg := range out.TopicConfig {
		if topic == "" || topicCfg.Mode < ModeNone || topicCfg.Mode > ModeBoth || topicCfg.PartitionNum > math.MaxInt32 {
			err = fmt.Errorf("tkkafka: invalid topic configuration for %q", topic)
			return
		}
		seen := make(map[string]bool, len(topicCfg.Groups))
		for _, group := range topicCfg.Groups {
			if group == "" || seen[group] {
				err = fmt.Errorf("tkkafka: empty or duplicate group for topic %q", topic)
				return
			}
			seen[group] = true
		}
		topicCfg.PartitionNum = cmp.Or(topicCfg.PartitionNum, 12)
		out.TopicConfig[topic] = topicCfg
	}
	return
}
