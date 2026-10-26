# tkhttp

基于标准库 `net/http` 的 HTTP 客户端，支持可选请求配置、连接池注入、调用层中间件、签名钩子、限额响应与一次调用一条累计日志。可并发复用，无第三方依赖。

每次调用最多执行一次 `http.Client.Do`，错误直接返回，不提供重试配置或依赖 `tkretry`。Transport 自身的连接处理沿用标准库语义。需要重试时由业务在外层显式组合独立组件；每次 tkhttp 调用分别计时和记录日志。

## 初始化与请求

基本参数通过 `Config` 传入，运行期依赖通过 With 选项注入。Client 建议在服务启动时创建并共享，退出时清理其自建池的空闲连接。

```go
client, err := tkhttp.New(
    tkhttp.Config{BaseURL: "https://api.example.com/v1/"},
    tkhttp.WithLogAcc(func() (acc adapter.LogAcc) {
        return logx.NewLogAccAdapter(logx.WithCost(time.Now()))
    }),
)
if err != nil {
    return err
}
defer client.CloseIdleConnections()

// 配置参数可省略，使用客户端默认值。
response, err := client.Get(ctx, "health")
if err != nil {
    return err
}
_ = response.Body // 正文已读取且关闭，调用方无需 Close。

var result map[string]any
response, err = client.Get(ctx, "devices", tkhttp.RequestConfig{
    Query:     url.Values{"page": {"1"}},
    Result:    &result,
    Timeout:   2 * time.Second,
    LogFields: map[string]any{"action": "queryDevices"},
})
if err != nil {
    return err
}
```

不需要日志时使用 `Config{BaseURL: baseURL, DisableLog: true}`，可省略 WithLogAcc。默认开启日志，缺少非 nil 工厂时 New 返回 `ErrInvalidConfig`。

| Config 字段 | 默认值和规则 |
| --- | --- |
| BaseURL | 必填 HTTP(S) 绝对地址，拒绝 userinfo、query、fragment |
| DefaultHeader | 默认无请求头；New 复制、校验并固定 |
| Timeout | 0 使用 10s；正数覆盖；精确 `time.Duration(-1)` 禁用组件超时，其他负数报错 |
| MaxResponseBytes | 0 使用 8 MiB；必须为正数且允许加一检测超限 |
| DisableLog | false，每次调用打印一条汇总日志 |

`Do(ctx, method, target, configs ...RequestConfig)` 支持任意合法方法，`Get`、`Post` 为便捷入口。RequestConfig 允许零或一份，多份直接返回 `ErrInvalidRequest`；省略与 `RequestConfig{}` 等价。

RequestConfig.Header 按大小写无关的字段名覆盖默认 Header；Query 合并目标 URL 的 query，同名键整体替换。JSON、Form、Body 三者互斥，分别提供 JSON、表单和字节正文。非 nil 空 Form/Body 仍表示显式请求体；JSON nil 表示未配置，需要 JSON null 时可传 `json.RawMessage("null")`。Content-Type 仅在未显式设置时自动补齐，原始字节不推测类型。

未提供 Query 时保留目标 URL 的原始 query；提供 Query 时按 url.ParseQuery 解析和合并，无法无损解析（如未转义分号）时直接报错。

Result 非 nil 时必须为非 nil 指针，仅解码成功且完整读取的响应。Timeout 为 0 时继承 Client，正数或精确 -1 仅覆盖本次调用；响应限额、日志开关和依赖使用 Client 设置。配置合并不修改客户端或传入配置。

New 和请求准备阶段均拒绝非法 Header、大小写不同的重复字段，以及由传输层管理的 Host、Content-Length、Transfer-Encoding、Trailer。调用方使用字段名规范的 `http.Header.Set/Add` 可避免重复问题。

## 连接池与依赖注入

```go
pool := &http.Transport{
    Proxy:                 http.ProxyFromEnvironment,
    MaxIdleConns:          200,
    MaxIdleConnsPerHost:   20,
    MaxConnsPerHost:       100,
    IdleConnTimeout:       90 * time.Second,
    TLSHandshakeTimeout:   10 * time.Second,
    ExpectContinueTimeout: time.Second,
    ForceAttemptHTTP2:     true,
}
defer pool.CloseIdleConnections() // 借用池由创建方管理。

client, err := tkhttp.New(
    tkhttp.Config{BaseURL: baseURL, DisableLog: true},
    tkhttp.WithTransport(pool),
)
if err != nil {
    return err
}
```

WithTransport 直接持有原指针，不调用 Clone，多个 Client 可共享实际连接。优先级固定为显式 WithTransport、WithHTTPClient 中的 Transport、自建默认池。选项顺序不改变优先级；单值依赖选项以后一次为准，WithMiddleware 按顺序追加。显式 nil 客户端、nil 池和最终选中的 typed nil Transport 返回配置错误。

未注入时，New 创建一个独立 `*http.Transport` 并供该 Client 所有请求复用，不依赖可被替换的全局 http.DefaultTransport。连接按需建立；默认采用环境代理，拨号和 TCP KeepAlive 30s，HTTP/2 协商开启，最大空闲连接 100，每主机空闲连接沿用标准库默认 2，单主机连接总数不限，空闲回收 90s、TLS 握手 10s、ExpectContinue 等待 1s。

WithHTTPClient 只复制 Transport、Jar、Timeout 等公开配置，创建私有 http.Client；Transport 和 Jar 仍为借用资源。注入客户端的 Transport 为 nil 时创建默认池。借用的 native Timeout 仍可提前终止请求；CheckRedirect 固定被覆盖为不跟随重定向，3xx 作为状态错误返回。

CloseIdleConnections 仅作用于自建池，借用池时为空操作，不取消正在执行的请求。注入池、Jar 和日志后端由调用方管理，投入使用后不要并发修改它们的配置。

## 调用层中间件

通过 WithMiddleware 注入完整调用层中间件，类型定义在 tkhttp 内。next 包含参数校验、请求编码、签名、发送、响应读取与关闭、状态检查和 JSON 解码，返回最终 Response/error。

```go
func observe(next tkhttp.Handler) (handler tkhttp.Handler) {
    return func(ctx context.Context, method, target string, configs ...tkhttp.RequestConfig) (response *tkhttp.Response, err error) {
        response, err = next(ctx, method, target, configs...)
        if err != nil {
            // 可通过 errors.Is/AsType 识别参数、读取、解码等错误。
            err = fmt.Errorf("调用失败: %w", err)
        }
        // response 非 nil 时，StatusCode 即为本次 HTTP 状态码。
        return
    }
}

// 日志开启时同时注入 WithLogAcc；此处演示独立使用中间件。
client, err := tkhttp.New(
    tkhttp.Config{BaseURL: baseURL, DisableLog: true},
    tkhttp.WithMiddleware(observe),
)
```

不注入或传空列表时保持原流程，nil 中间件在 New 返回 ErrInvalidConfig。选项和 New 均复制中间件列表，不受调用方后续修改影响。多次 WithMiddleware 按顺序追加；注册 A、B 时，执行 A 前置、B 前置、核心调用、B 后置、A 后置，最后打印唯一的汇总日志。

中间件包装函数在每次请求到达该层时执行，New 不调用它；适合轻量创建本次 Handler，共享资源应在创建 Middleware 时初始化。共享状态必须并发安全，每次调用的执行状态和 LogAcc 独立。

中间件前置逻辑可以修改传给 next 的参数；修改 Header/Query/Body/LogFields 前应复制对应容器。修改后的参数仍需通过核心校验，签名钩子随后执行。原始 configs 数量、nil/typed nil context 等校验错误也会返回给 next 的调用方。

允许提前返回错误或合成 Response。合成成功响应时核心不会执行网络、状态检查和 JSON 解码，中间件负责提供合法响应及 Result。返回 nil Handler、nil Response 且 nil error，或违反 next 调用约定时返回 ErrMiddleware。每层 next 必须同步且最多调用一次；重复、构造期间及返回后的调用被拒绝。若 Handler 返回时 next 仍在执行，会等待其结束并返回 ErrMiddleware，避免与最终日志竞争。中间件 panic 继续向上传播。

用 %w 包装 next 的错误可保留错误链。汇总日志使用整条链最终返回的 Response/error，记录前后置总耗时；中间件消除或替换错误后，日志阶段和级别随最终结果更新。配置 Timeout 仍由核心按入口时间计算，中间件应配合传入的调用方 context；同步代码不能被强制中断。

## 累计日志

`adapter.LogAcc` 是共享契约，`tkhttp` 不导入具体日志实现。`logx.NewLogAccAdapter(opts...)` 可创建适配实例，也可注入其他满足契约的实现。接口和具体 LogAcc 都不提供 Clone，每次调用由工厂创建新实例。

```go
// backend 为 adapter.Logger，可使用 GoFrame 适配器，或 logx.NewLoggerAdapter(logger)。
var factory tkhttp.LogAccFactory = func() (acc adapter.LogAcc) {
    acc = logx.NewLogAccAdapter(logx.WithLogger(backend), logx.WithCost(time.Now()))
    return acc.SetValue("component", "deviceAPI")
}
// 初始化时通过 tkhttp.WithLogAcc(factory) 注入。
```

`LogAccFactory` 的签名为 `func() (acc adapter.LogAcc)`，仅供 tkhttp 使用，声明在本包中。New 校验工厂非 nil，不调用工厂；每次 HTTP 调用执行一次工厂，再通过 SetStartTime 设置本次入口时间。工厂必须支持并发调用，并返回字段、起点和 Skip 均独立的新累加器，可以共享日志后端。

`logx.WithLogger` 接收共享的 `adapter.Logger`，无需绑定 logx Logger。完整 GoFrame 后端与 HTTP 工厂示例见 [GoFrame 接入](../../examples/logacc_goframe/README.md)。

每次调用独立累计 `http` 元数据和 `biz` 业务字段，在统一出口只调用一次 Info/Warn/Error。成功用 Info，3xx/4xx 和主动取消用 Warn，其他失败用 Error；参数错误同样记录，初始化错误由调用方处理。

`http` 包含 method、脱敏 url（仅 scheme/host/path）、statusCode、requestBytes、responseBytes、cost（可读耗时字符串）、phase、errorKind。默认不记录 query 值、请求/响应正文、Header、鉴权值或原始错误文本。业务字段仅放入 biz，不覆盖内置 http 组。trace 等 context 字段由注入实现提取，取消后仍传原 context 打印。不得通过业务字段或工厂设置的公共字段写入不希望输出的数据。

容器在调用时复制；LogFields 的嵌套 map、slice、array 也复制。指针、结构体中的引用对象和 JSON 输入仍需由调用方保证调用期间不变，同一个 Result 不能被并发写入。工厂设置的公共字段若引用可变对象，也需自行保证同步。

关闭日志时不调用工厂或设置起点。显式传入 nil 工厂仍视为配置错误；工厂返回 nil/typed nil 时，请求返回 `ErrLogAcc` 且不发网络请求，此时没有有效累加器可打印。调用方、工厂或实现 panic、日志后端写入失败不属于正常返回时一次输出调用的保证。

## 签名钩子

WithRequestHook 在发送前执行一次，只返回要补充或覆盖的 Header。输入 Method、完整 URL、Header 和 Body 为快照；修改快照不会修改真实请求。正文只编码一次，签名钩子看到的 Body 与发送字节一致。

```go
hook := func(ctx context.Context, info tkhttp.RequestInfo) (headers http.Header, err error) {
    // 签名串仅作示例，实际方法、路径、query 和正文规则由平台协议确定。
    signature, err := signer.SignString(info.Method + "\n" + info.URL + "\n" + string(info.Body))
    if err != nil {
        return
    }
    return http.Header{"X-Signature": {signature}}, nil
}
// 初始化时传入 tkhttp.WithRequestHook(hook)，signer 使用已有 tksign.Signer。
```

钩子应配合 context 及时返回；钩子错误或无效 Header 以 `ErrRequestHook` 返回，不进入网络阶段。Jar/Transport 自动添加的字段不在钩子输入中。

## 响应、错误与超时

2xx 成功，其他状态返回 `*StatusError`，同时保留 Response。响应头复制，正文限额读取并始终关闭，默认限额按实际读取的字节计，自动解压时为解压后内容。读取上限加一用于识别超限；超限返回 `ErrResponseTooLarge` 和限额内前缀，BodyTruncated 为 true，不无界读取剩余数据。

未收到响应时 Response 为 nil；读取或解码失败仍返回已取得的 Response。读取错误优先于状态错误。Result 使用 json.Unmarshal，拒绝尾随第二个 JSON 值；HEAD、204、205 不解码，其他成功状态在要求解码时遇到空正文会报错。解码失败可能部分写入 Result，不能视为完整结果。

Response.StatusCode 为收到的 HTTP 状态码，即使正文读取、超限或 JSON 解码失败也会保留；未收到响应时 Response 为 nil。非 2xx 同时返回 StatusError，可以通过 `errors.AsType[*tkhttp.StatusError](err)` 获取其中的 StatusCode。

包级错误、自定义类型及包装助手统一声明在 [error.go](error.go)。`*RequestError` 提供 Phase 和 Unwrap；通过 errors.Is 判断稳定类别，通过 errors.AsType 获取状态码或底层网络/JSON 原因。Error 文本保持受控，显式解包后应自行决定如何处理原始错误。

阶段常量集中声明在 [phase.go](phase.go)，用于判断 `RequestError.Phase` 和日志的 `http.phase`。常量保留原有字符串值，`RequestError.Phase` 仍为 `string`，可直接与常量比较。

| 阶段常量 | 值 | 含义 |
| --- | --- | --- |
| PhasePrepare | prepare | 参数校验与请求准备 |
| PhaseHook | hook | 签名或鉴权钩子 |
| PhaseTransport | transport | HTTP 发送 |
| PhaseRead | read | 响应正文读取 |
| PhaseStatus | status | HTTP 状态码检查 |
| PhaseDecode | decode | JSON 响应解码 |
| PhaseComplete | complete | 整次调用成功完成 |
| PhaseMiddleware | middleware | 中间件失败或违反调用约定 |

```go
failure, ok := errors.AsType[*tkhttp.RequestError](err)
if ok && failure.Phase == tkhttp.PhaseRead {
    // 响应读取失败；已有响应时仍可从 response 获取状态码。
}
```

非 2xx 返回 `StatusError`，对应日志阶段为 `PhaseStatus`；成功调用的日志阶段为 `PhaseComplete`。

| 错误 | 含义 |
| --- | --- |
| ErrInvalidConfig | 客户端配置或注入依赖无效 |
| ErrInvalidRequest | 请求参数校验失败 |
| ErrRequestEncode | 请求正文编码失败 |
| ErrRequestHook | 签名或鉴权钩子失败 |
| ErrTransport | HTTP 发送失败 |
| ErrResponseRead | 响应读取失败，包括超限 |
| ErrResponseTooLarge | 超过响应读取限额，同时匹配 ErrResponseRead |
| ErrHTTPStatus | 非 2xx，具体码在 StatusError/Response 中 |
| ErrResponseDecode | JSON 解码失败 |
| ErrLogAcc | 日志工厂返回 nil/typed nil |
| ErrMiddleware | 中间件失败、无效结果或违反 next 约定 |

取消和超时直接匹配 context.Canceled/context.DeadlineExceeded；所有封装保留底层原因。中间件自行返回的普通错误会归入 middleware 阶段，透传或通过 %w 包装的核心错误保留原阶段。

组件总超时从调用入口计时，包含参数准备、签名、HTTP、读取和解码；父 context 更早截止时优先，精确 -1 不取消父时限或借用客户端的 Timeout。网络和连接池等待可取消，同步编码/解码或不配合 context 的钩子不能被强制中断，会在返回后检查取消状态。ctx 必须非 nil；无效 ctx 只在错误日志中使用 Background，不发起请求。

BaseURL 使用标准库 ResolveReference：`https://example.com/v1/` + `orders` 得到 `/v1/orders`，`/orders` 得到根路径。允许相对路径及同源绝对地址，拒绝跨源地址、userinfo、fragment 和 `//host/path`；不自动跟随重定向。
