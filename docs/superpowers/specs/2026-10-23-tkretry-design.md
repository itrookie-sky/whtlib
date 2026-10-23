# tkretry 通用重试组件设计

## 目标与边界

在 `toolkit/tkretry` 提供独立、可并发复用的通用重试执行器，由调用方显式组合任务或请求。tkhttp 不依赖、配置或调用本组件；重试是否安全以及哪些错误允许重试，由业务调用方判断。

组件只依赖标准库，不依赖 net/http、tkhttp、adapter 或 logx。负责最大尝试次数、指数退避、full jitter、等待上限、context 取消和通用观察事件；配置、决策及事件不包含 HTTP 状态码或响应类型。

## 通用契约

以下为拟定公开 API，尚未实现：

```go
type Config struct {
	MaxAttempts int           // 最大尝试次数，包含首次执行。
	BaseDelay   time.Duration // 指数退避的初始上限。
	MaxDelay    time.Duration // 单次重试等待的最大时长。
}

type Decision struct {
	Retry    bool          // 本次错误是否允许重试，由调用方判断。
	MinDelay time.Duration // 下一次执行前至少等待的时长，0 表示无额外下限。
}

func New(config Config) (retry *Retry, err error)
func (r *Retry) Do(ctx context.Context, operation func(ctx context.Context, attempt int) (decision Decision, err error), opts ...RunOption) (err error)
func WithObserver(observer func(event Event)) (option RunOption)
```

MaxAttempts 为 0 时补齐 1，BaseDelay 为 0 时补齐 200ms，MaxDelay 为 0 时补齐 2s；负数或 MaxDelay 小于 BaseDelay 返回配置错误。配置在 New 中归一化并固定，同一 Retry 实例可并发复用，每次 Do 的状态独立。

Do 的 operation 每次只执行一次调用，attempt 从 1 开始。err 为 nil 时完成执行，忽略重试决定；有错误时仅在 Decision.Retry 为 true 且次数、等待上限、剩余时限允许时再试。未允许重试或尝试次数耗尽时保留最后一次操作错误，context 终止时保留可供 errors.Is 判断的取消或截止错误。nil context 或 nil operation 直接返回参数错误；仅在需要重试时校验 MinDelay，负值返回参数错误。

指数退避采用 full jitter，在 `[0, min(MaxDelay, BaseDelay × 2^n)]` 内选择等待，n 从第一次重试的 0 开始，指数计算饱和防溢出。实际计划等待取随机退避与 MinDelay 的较大值；超过 MaxDelay 或剩余总时限时停止，不缩短调用方要求的最小等待。等待统一采用可取消计时器，取消后不发起下一次执行；同步 operation 需配合 context 返回，不能被强制抢占。

Observer 是每次 Do 的可选依赖，未注入时无观察回调。Event 记录通用的事件种类（attempt、wait、stop）、序号、执行耗时、实际等待、错误和停止原因，不携带 HTTP 类型。回调在当前执行流程中同步触发，同一实例的并发 Do 各自持有自己的观察回调。tkretry 本身不输出日志，调用方通过观察回调按需累计业务字段和尝试过程。观察回调应快速返回，错误和事件中包含的操作数据不由 tkretry 向外打印。

## 调用方职责

调用方每次执行回调只完成一次业务操作，并通过 Decision 返回是否允许重试与额外等待下限。业务幂等性、HTTP 状态码、平台限流提示或消息消费规则均由调用方处理。需要重试 HTTP 调用时，由业务在回调中调用一次 tkhttp；tkhttp 仍独立完成本次计时、响应处理和日志。

重试总预算通过传入的 context 控制，单次操作可以进一步设置更短的时限。观察逻辑由本次 Do 的调用方注入，避免多个并发执行共享可变状态。

## 文件组织与验证建议

建议在 `toolkit/tkretry/` 按 `config.go`、`retry.go`、`backoff.go`、观察事件和 README 组织，保持单向的业务调用关系，不为 tkhttp 添加反向依赖或自动重试入口。

获准编写测试后，验证默认值、非法配置、次数上限、full jitter 范围、MinDelay、超时取消、观察事件、并发 Do 的状态隔离以及错误链。验证使用本地回调和可控等待，不连接外部服务；仅对本包运行 race 和 vet。本次仅编写设计文档，未实现组件。

## 参考

- [Go context：截止时间与取消](https://pkg.go.dev/context)
- [AWS Builders’ Library：超时、重试、退避与随机抖动](https://aws.amazon.com/builders-library/timeouts-retries-and-backoff-with-jitter/)
