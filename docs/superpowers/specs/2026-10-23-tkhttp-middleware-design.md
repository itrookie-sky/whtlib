# tkhttp 调用层中间件设计

状态：方案待确认，尚未修改实现。

## 目标与范围

用户已确认：中间件通过 With 可选参数注入，需要拿到参数校验、响应读取和 JSON 解码的最终错误。因此采用完整调用层中间件，终端处理函数包含 tkhttp 原有执行流程。

中间件仅供 tkhttp 使用，类型声明在本包。Config 继续存放基本参数，WithMiddleware 负责注入依赖。现有连接池、RequestHook、可选 RequestConfig 和日志工厂继续复用。

## 方案选择

| 方案 | 能力与代价 | 结论 |
| --- | --- | --- |
| 包装 http.RoundTripper | 直接复用标准库类型，但看不到 tkhttp 的参数校验、响应读取和 JSON 解码错误 | 不满足本次范围 |
| 包装 tkhttp 完整调用 | 能读取最终 Response 和 error，需要定义处理函数与中间件类型 | 本次采用 |

## 建议接口

```go
// Handler 执行一次完整调用，返回已读取并关闭原始正文后的响应快照及最终错误。
type Handler func(
    ctx context.Context,
    method, target string,
    configs ...RequestConfig,
) (response *Response, err error)

// Middleware 包装完整调用；其返回的处理函数处理一次请求。
type Middleware func(next Handler) (handler Handler)

// WithMiddleware 按顺序追加调用层中间件；不传时使用原有流程。
func WithMiddleware(middlewares ...Middleware) (option Option)
```

Handler 沿用现有 Do 的入参，不新增 Request 容器。原始 configs 进入处理链后才进行数量、字段与 JSON 输入校验，保证中间件可以观察对应错误。nil/typed nil context 同样交由终端校验，不在进入处理链前返回。

```go
client, err := tkhttp.New(
    tkhttp.Config{BaseURL: baseURL},
    tkhttp.WithLogAcc(factory),
    tkhttp.WithMiddleware(observe, authenticate),
)
```

不传中间件或传入空列表时保持原行为。多次 WithMiddleware 按出现顺序追加，区别于现有单值依赖选项的后者覆盖前者。传入 nil 中间件时 New 返回 ErrInvalidConfig。

## 执行顺序

每次公开调用在入口记录开始时间，创建独立 LogAcc 并注册唯一的最终日志出口，然后执行中间件链。注册 A、B 时，A 在最外层，执行顺序为 A 前置、B 前置、核心调用、B 后置、A 后置，最后打印汇总日志。

核心调用依次执行：参数校验、合并默认配置、构造请求及编码正文、RequestHook、发送、限量读取并关闭响应、HTTP 状态检查、JSON 解码。中间件前置操作修改的请求参数会先经过校验，再进入签名钩子，避免在签名完成后修改待签名内容。

建议在 New 时复制并冻结中间件列表，每次调用使用独立状态与终端闭包组装处理链，不向 context 写入可变内部状态。Middleware 包装函数每次调用执行，应只做轻量包装；长期共享资源应在创建 Middleware 时初始化，相关状态由实现保证并发安全。

## 结果与错误

中间件在 next 返回后能直接读取以下结果：

| 结果 | 保持的现有语义 |
| --- | --- |
| 参数无效、配置数量超限、正文编码失败 | prepare 阶段的 RequestError |
| 签名或鉴权钩子失败 | hook 阶段，保留 ErrRequestHook 和原始错误 |
| 发送失败、取消或超时 | 对应阶段的 RequestError，保留 errors.Is 判断能力 |
| 正文读取失败或超限 | read 阶段；可附带部分 Response，保留 ErrResponseTooLarge |
| 非 2xx 响应 | StatusError，附带已读取的 Response |
| JSON 解码失败 | decode 阶段；附带原始响应快照 |
| 调用成功 | 非 nil Response 和 nil error |

中间件应返回 next 的结果，或使用 %w 包装错误，保留 errors.Is/AsType 能力。中间件可以提前返回错误，中断后续执行。自造错误增加 ErrMiddleware 标识和 middleware 阶段，已有核心错误不重复改写阶段。

中间件也可提前返回有效 Response；此时不执行网络请求、核心状态检查或 JSON 解码，中间件负责提供与调用约定一致的响应及 Result。返回 nil Response 且 error 为 nil 视为无效中间件结果，返回 ErrMiddleware。每层 next 约定最多调用一次且同步返回；不通过此能力增加 tkhttp 内置重试。

中间件返回 nil Handler 属于运行期构造失败，返回 ErrMiddleware，不发请求。调用方或中间件 panic 沿用现有传播方式，不新增 recover。

## 错误集中声明与封装

用户已确认：包内所有具有封装价值的错误，在独立文件中统一声明。沿用现有 `toolkit/tkhttp/error.go`，集中维护导出的哨兵错误、自定义错误类型、Error/Unwrap/Is 方法及内部构造、包装助手，不在其他业务文件中零散声明。

错误类别按调用方可以稳定判断和处理的边界划分，建议完整集合如下：

| 哨兵错误 | 含义 |
| --- | --- |
| ErrInvalidConfig | 客户端配置或注入依赖无效，包括 nil 中间件 |
| ErrInvalidRequest | 请求参数无效，包括配置数量、地址、请求头和输入冲突 |
| ErrRequestEncode | 请求正文编码失败，保留具体编码错误 |
| ErrRequestHook | 签名或鉴权钩子执行失败，保留原始原因 |
| ErrTransport | 网络发送阶段失败，保留原始网络或 context 错误 |
| ErrResponseRead | 响应读取失败，包括超出读取限额 |
| ErrResponseTooLarge | 响应超过配置读取限额，同时归属 ErrResponseRead |
| ErrHTTPStatus | HTTP 状态码非 2xx，具体状态码由 StatusError 提供 |
| ErrResponseDecode | JSON 响应解码失败，保留具体 JSON 错误 |
| ErrLogAcc | 工厂返回 nil/typed nil 日志累加器 |
| ErrMiddleware | 中间件执行失败、返回 nil Handler、返回无效结果或违反调用约定 |

保留已有错误标识及错误类型，不改变其字段类型或取消原有匹配能力。`RequestError` 继续使用 Phase 和 Err 描述失败阶段及原因，支持 Unwrap；Phase 扩展 middleware。`StatusError` 保留 StatusCode，并通过 Is 支持 `errors.Is(err, ErrHTTPStatus)`，调用方仍可通过 `errors.AsType[*StatusError]` 获取状态码。

阶段错误增加包级类别时必须保留底层错误链。例如读取异常可封装为 `RequestError{Phase: "read", Err: errors.Join(ErrResponseRead, cause)}`；超限时同时保留 ErrResponseTooLarge。中间件透传或通过 %w 包装后，调用方仍能判断包级类别以及具体原因，不能只把错误转为字符串。

`context.Canceled`、`context.DeadlineExceeded` 直接复用标准库标识，不另造 ErrCanceled 或 ErrTimeout；网络、I/O、JSON 错误类型保留在错误链中。具体参数校验原因继续作为 ErrInvalidRequest 或 ErrInvalidConfig 的内部说明，不为每个字段、每条校验文本新增导出变量。

对外错误文本保持现有约束：RequestError.Error 不泄露 URL、正文或底层原始错误文本；StatusError.Error 仅包含状态码。原始原因供调用方显式解包使用，汇总日志按分类记录，不直接打印可能包含敏感数据的底层错误。

## 日志、超时和资源

汇总日志位于中间件链外层，使用整条链最终返回的 Response 和 error，包含前置及后置耗时。中间件提前拒绝或改变返回错误，也只打印一次汇总日志；日志阶段应反映最终结果，不能在错误被处理中消除后保留失败分类。

缺少有效日志工厂等初始化错误仍在 New 返回；工厂返回 nil/typed nil 的调用期基础设施错误仍在进入中间件前返回 ErrLogAcc。中间件观察范围以已成功创建的 Client 和有效调用日志设施为前提；关闭日志时仍可使用中间件。

超时配置继续由核心执行流程处理；中间件应响应传入的调用方 context，不使用额外 goroutine 强行中断中间件。原始响应正文仍由核心读取并关闭，中间件拿到 Response.Body 字节快照，无需再次关闭。

中间件列表与连接池可以共享；每次调用的日志和执行状态独立。中间件修改配置中的 Header、Query、Body 或 LogFields 前应复制相应容器，不改变调用方的共享配置。

## 观察错误的示例

```go
func observe(next tkhttp.Handler) (handler tkhttp.Handler) {
    return func(ctx context.Context, method, target string, configs ...tkhttp.RequestConfig) (response *tkhttp.Response, err error) {
        response, err = next(ctx, method, target, configs...)
        if err != nil {
            // 此处可记录指标或判断业务错误，包含参数、读取及解码错误。
            // 使用 errors.Is/AsType 检查；需要附加上下文时使用 %w 包装。
            return
        }
        return
    }
}
```

## 后续实现与验证范围

实现将涉及 options.go、client.go、独立 middleware.go、集中错误文件 error.go、日志分类及 README。先保留现有 RequestHook，避免要求调用方迁移签名实现。业务代码复用 error.go 的错误声明和包装助手。

相关验证应覆盖注册顺序、空列表、nil 中间件、提前拒绝、无效结果、全部核心错误透传、包级错误分类与原始原因的 errors.Is/AsType、日志一次输出及并发隔离。沿用本次对话已获批准的 tkhttp/LogAcc 相关测试范围，仅运行受影响包，不连接外部平台或数据库。
