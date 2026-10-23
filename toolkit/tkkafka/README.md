# tkkafka

基于 `confluent-kafka-go/v2` 的 Kafka 组件，提供生产、单条消费、批量消费、失败重试、按分区确认及资源关闭。依赖标准库、现有 `adapter.Logger` 和 Kafka 驱动，不依赖 GoFrame 或 `go-gf-lib`。

## 使用

```go
import (
    "context"
    "time"

    "github.com/itrookie-sky/whtlib/toolkit/tkkafka"

    "github.com/confluentinc/confluent-kafka-go/v2/kafka"
)

func run(ctx context.Context) (err error) {
    client, err := tkkafka.New(&tkkafka.Config{
        Servers: "127.0.0.1:9092",
        Env: "prod",
        ConsumerEnv: "worker",
        RetryMaxCount: 3,
        RetryDelay: time.Second,
        TopicConfig: map[string]tkkafka.TopicConfig{
            "orders": {Mode: tkkafka.ModeBoth, PartitionNum: 12},
        },
    }, tkkafka.WithCloseTimeout(10*time.Second))
    if err != nil {
        return
    }
    defer client.Close()

    // 等待驱动的最终投递确认。
    _, err = client.SendSync(ctx, "orders", &tkkafka.ProducerMessage{
        Key: "order-42",
        Data: map[string]any{"id": 42, "created_at": "preserved"},
    })
    if err != nil {
        return
    }

    // 业务函数应响应 ctx；这里仅展示调用入口。
    subscription, err := client.SubscribeContext(ctx, "orders",
        func(ctx context.Context, message *kafka.Message) (handlerErr error) {
            // 解析和处理 message.Value，成功才返回 nil。
            return ctx.Err()
        },
    )
    if err != nil {
        return
    }
    return subscription.Wait(ctx)
}
```

`New` 只校验和复制配置，不连接 broker。第一次发送或订阅按需创建驱动资源；也可以提前调用 `NewProducer`、`NewConsumer`。需要观测 flush 失败时，应显式处理 `Close` 返回值。

## 配置与路由

原 `Config`、`TopicConfig`、`ProducerMessage` 的公开字段类型和 JSON 名称保持不变。`New`、`NewWithConfig` 和 `NewWithOption` 共用默认值处理，不修改调用方配置，`Config()` 返回独立快照。

| 配置 | 默认值及单位 |
| --- | --- |
| `Servers`、`Protocol` | `127.0.0.1:9092`、`PLAINTEXT` |
| `Retries`、`RetryBackoff` | `2147483647` 次、`1000` 毫秒；非正数使用默认值 |
| `LingerMs`、`CompressionType`、`MessageMaxBytes` | `100` 毫秒、`gzip`、`16384` 字节 |
| `WaitTimeout` | `time.Duration(-1)`；内部仍分段 poll，允许检查取消 |
| `RetryDelay`、`RetryMaxCount` | `10*time.Second`；`0` 表示无限重试，正数为首次失败后的额外尝试次数 |
| `BatchSize` | `200`；沿用原行为，同时作为生产端 `batch.size` 字节数与消费端每批消息数 |
| `BatchInterval` | `5*time.Second`，从批次第一条消息开始计时 |
| `Env`、`ConsumerEnv` | `local`；消费者环境默认跟随 `Env` |
| `TopicConfig.PartitionNum` | `12`，每个消费组创建同等数量的消费者 |
| `TopicConfig.Mode` | `ModeNone`；需要显式选择 `ModeProducer`、`ModeConsumer` 或 `ModeBoth` |

topic 为 `<Env>_<topic>`，消费组为 `<ConsumerEnv>_group_<name>`。未配置 `Groups` 时，`name` 使用 topic；配置 `Groups` 时使用各组后缀。不传订阅 group 会启动该 topic 的全部已配置组，显式传入时必须属于该 topic 的组列表。

非空消息 key 使用 FNV-1a 对 `PartitionNum` 取模；空 key 使用 `kafka.PartitionAny`。分区数应与 broker 中实际 topic 一致，本包不自动创建 topic 或推断分区数。

`GlobalProducer` 非空时，多个 topic 共享一个生产者。`ExcludeTopics` 阻止对应 topic 发送；发送返回 `ErrExcluded`，不会伪装为排队成功。`IsClose` 或不匹配的 topic 模式返回 `ErrDisabled`。预创建方法遇到禁用模式则不创建资源。

源实现没有将 `BufferMemory` 下传给驱动。本包为非零值明确采用字节单位，向上取整为 KiB 后映射到 `queue.buffering.max.kbytes`；零值保留驱动默认配置。消费端关闭 `enable.auto.commit` 和 `enable.auto.offset.store`，沿用原 `max.poll.interval.ms=60000`。

## 编码与发送结果

默认 `JSONEncoder` 直接使用 `encoding/json`，保留数据库时间字段，不修改业务数据。`WithEncoder` 可注入并发安全的自定义编码器，客户端会复制返回字节再排队。

`WithEncoder(tkkafka.LegacyEncoder{})` 可显式过滤 JSON 对象顶层的 `created_at`、`updated_at`、`deleted_at`、`create_time`、`update_time`、`delete_time`，保持嵌套字段与数字精度，且不修改输入 map。此入口仅提供明确的 JSON 字段兼容，不复现 `gconv.Map` 对任意 Go 对象的全部转换规则；迁移旧消息前仍需核对样本。

| 方法 | 返回边界 |
| --- | --- |
| `SendMessage` | 兼容旧签名，只返回编码或排队错误 |
| `Send` | 立即返回排队错误，以及容量为 1 的最终 `DeliveryResult` 通道；每次成功排队恰好发送一个结果并关闭通道 |
| `SendSync` | 等待最终 delivery，驱动投递错误同时出现在结果 `Err` 和方法返回的 error 中 |

`Send` 排队前检查 ctx，排队后 ctx 取消不会撤回消息；即使调用方不读结果通道，后台任务也能退出。`SendSync` 的 ctx 还限制等待时间，等待超时不表示消息一定没有发送。发送期间的驱动初始化和 `Produce` 为同步调用，不支持强行抢占。

本包沿用 `acks=1`，delivery 成功的持久化强度取决于 Kafka 的复制配置。关闭前未获得最终确认时返回 `ErrUndelivered`，消息可能已到达 broker；重发应考虑业务幂等。

## 消费、重试与关闭

`Subscribe`、`BatchSubscribe` 保留旧回调形式，返回启动错误。新代码可使用 `SubscribeContext`、`BatchSubscribeContext`，通过 `Subscription.Wait(ctx)` 可靠观察任务结果，使用 `Stop()` 请求取消。

消费在各驱动内串行处理，成功后同步提交；多 partition 批次分别提交各分区成功处理的最大 offset 加一，不将整批末尾消息当成全部分区的进度。Kafka 自身的稀疏 offset 可以跨越。提交位置在调用业务代码前独立保存，不受回调修改消息路由字段影响。

失败处理遵循以下规则：

- 单条或整批 handler 失败时等待 `RetryDelay` 后重试，等待可由 ctx 或关闭取消。
- 有限重试耗尽后终止整个订阅，向 `Subscription.Wait` 暴露原始错误，失败消息或失败批次均不提交。
- 提交错误，包括单个 partition 的提交错误，终止订阅并暴露；业务已成功而提交失败的记录可能重放。
- ctx 取消或关闭时不处理残留批次，也不确认取消期间尚未完成的处理。handler 返回后会再次检查 ctx。
- handler panic 会转换成处理失败并遵循同样的重试策略。

消费者正常 poll 默认最多阻塞 `100ms`，可用 `WithPollInterval` 调整；正数 `WaitTimeout` 也会限制单次 poll。handler 及其重试期间不拉取新记录，长处理可能超过 `max.poll.interval.ms` 并触发重平衡；业务应控制处理耗时和重试预算。无限重试不是无限期持有分区的保证。

同一消费实例不能重复订阅。订阅结束后其驱动会关闭；要重启该订阅，应新建客户端。不同 topic 即使配置相同 group 后缀也分别管理资源。消费与外部数据库写入不属于同一事务，不承诺 exactly-once。

`Close()` 或 `Close(ctx)` 并发安全，关闭请求会立即阻止新资源注册、取消消费和重试，按 `WithCloseTimeout` 的总预算 flush 生产者，然后等待任务并关闭驱动。调用方等待同时受该超时及传入 ctx 限制。无法强行终止不响应取消的 handler 或正在执行的驱动操作；等待超时返回 `context.DeadlineExceeded`，清理会继续进行，可再次调用 `Close` 等待。不要在 handler 内同步等待当前客户端的 Close 完成。

`WithLogger` 可选注入 `adapter.Logger`；不创建全局日志，不主动输出消息正文或认证配置。`Errors()` 是容量为 128 的非阻塞诊断流，满时丢弃新诊断，全部任务退出后关闭；可靠结果应从 delivery 通道和 `Subscription.Wait` 获取。

## 错误处理

包级错误和包装助手集中在 [error.go](error.go)，使用 errors.Is 匹配类别，使用 errors.AsType 获取原始 Kafka/JSON 错误。既有 ErrClosed、ErrDisabled、ErrExcluded、ErrAlreadySubscribed、ErrUndelivered 保持原有语义。

| 错误 | 含义 |
| --- | --- |
| ErrInvalidConfig | 配置或注入依赖无效 |
| ErrInvalidArgument | 消息、handler 或消费组参数无效 |
| ErrTopicNotConfigured | 请求的 topic 未配置 |
| ErrEncode | 消息编码失败，包含自定义 Encoder |
| ErrDelivery | Send 排队或最终 delivery 结果失败 |
| ErrCommit | 消费位点提交失败 |
| ErrHandler | 消费回调执行失败 |
| ErrHandlerPanic | 消费回调 panic，同时匹配 ErrHandler |

编码、投递、位点提交和处理失败均保留底层原因。panic 值本身为 error 时也保留错误链；context 取消和超时继续复用标准库错误。原生诊断事件仍可通过 Kafka 错误类型及错误码判断。

## 验证边界

模块测试使用内部 fake 驱动，不连接真实 Kafka，覆盖配置克隆、编码与分区、排队及最终确认、重试耗尽不提交、多分区确认、取消与资源清理。构建保留 Kafka 驱动的 CGO 和目标平台原生依赖要求。

```sh
go test -race ./toolkit/tkkafka
go vet ./toolkit/tkkafka
```

真实 broker 的认证、分区元数据、重平衡、断线恢复与最终持久化仍需在业务环境验证。
