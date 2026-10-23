# tkhttp

基于标准库 `net/http` 的 HTTP 客户端，支持可选请求配置、连接池注入、签名钩子、限额响应与一次调用一条累计日志。可并发复用，无第三方依赖。

每次调用最多执行一次 `http.Client.Do`，错误直接返回，不提供重试配置或依赖 `tkretry`。Transport 自身的连接处理沿用标准库语义。需要重试时由业务在外层显式组合独立组件；每次 tkhttp 调用分别计时和记录日志。

## 初始化与请求

基本参数通过 `Config` 传入，运行期依赖通过 With 选项注入。Client 建议在服务启动时创建并共享，退出时清理其自建池的空闲连接。

```go
client, err := tkhttp.New(
    tkhttp.Config{BaseURL: "https://api.example.com/v1/"},
    tkhttp.WithLogAcc(logx.NewLogAccAdapter(logx.WithCost(time.Now()))),
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

不需要日志时使用 `Config{BaseURL: baseURL, DisableLog: true}`，可省略 WithLogAcc。默认开启日志，缺少有效模板时 New 返回 `ErrInvalidConfig`。

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

WithTransport 直接持有原指针，不调用 Clone，多个 Client 可共享实际连接。优先级固定为显式 WithTransport、WithHTTPClient 中的 Transport、自建默认池。选项顺序不改变优先级；同类选项以后一次为准。显式 nil 客户端、nil 池和最终选中的 typed nil Transport 返回配置错误。

未注入时，New 创建一个独立 `*http.Transport` 并供该 Client 所有请求复用，不依赖可被替换的全局 http.DefaultTransport。连接按需建立；默认采用环境代理，拨号和 TCP KeepAlive 30s，HTTP/2 协商开启，最大空闲连接 100，每主机空闲连接沿用标准库默认 2，单主机连接总数不限，空闲回收 90s、TLS 握手 10s、ExpectContinue 等待 1s。

WithHTTPClient 只复制 Transport、Jar、Timeout 等公开配置，创建私有 http.Client；Transport 和 Jar 仍为借用资源。注入客户端的 Transport 为 nil 时创建默认池。借用的 native Timeout 仍可提前终止请求；CheckRedirect 固定被覆盖为不跟随重定向，3xx 作为状态错误返回。

CloseIdleConnections 仅作用于自建池，借用池时为空操作，不取消正在执行的请求。注入池、Jar 和日志后端由调用方管理，投入使用后不要并发修改它们的配置。

## 累计日志

`adapter.LogAcc` 是共享契约，`tkhttp` 不导入具体日志实现。`logx.NewLogAccAdapter(opts...)` 可直接创建适配实例，也可注入其他满足契约且支持独立 Clone 的实现。

```go
template := logx.NewLogAccAdapter(logx.WithLogger(logger), logx.WithCost(time.Now()))
template.SetStartTime(time.Now()).SetValue("component", "deviceAPI")
```

New 先克隆注入模板，每次调用再克隆一次并设置本次入口时间，不声明 Factory 函数类型。每次调用独立累计 `http` 元数据和 `biz` 业务字段，在统一出口只调用一次 Info/Warn/Error。成功用 Info，3xx/4xx 和主动取消用 Warn，其他失败用 Error；参数错误同样记录，初始化错误由调用方处理。

`http` 包含 method、脱敏 url（仅 scheme/host/path）、statusCode、requestBytes、responseBytes、costMs、phase、errorKind。默认不记录 query 值、请求/响应正文、Header、鉴权值或原始错误文本。业务字段仅放入 biz，不覆盖内置 http 组。trace 等 context 字段由注入实现提取，取消后仍传原 context 打印。不得通过业务字段或模板字段写入不希望输出的数据。

容器在调用时复制；LogFields 的嵌套 map、slice、array 也复制。指针、结构体中的引用对象和 JSON 输入仍需由调用方保证调用期间不变，同一个 Result 不能被并发写入。LogAcc.Clone 自身为字段 map 的浅拷贝，模板中的引用值应保持不变。

关闭日志时不克隆或设置模板。Clone 返回 nil/typed nil 时返回 `ErrLogAcc` 且不发网络请求；该情况下没有有效累加器可打印。调用方或实现 panic、日志后端写入失败不属于正常返回时一次输出调用的保证。

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

`*RequestError` 提供 Phase 和 Unwrap；用 `errors.Is` 判断 ErrInvalidRequest、ErrRequestHook、ErrResponseTooLarge、context.Canceled、context.DeadlineExceeded，用 `errors.AsType` 检查状态码或底层网络/JSON 原因。Error 文本保持受控，显式解包后应自行决定如何处理原始错误。

组件总超时从调用入口计时，包含参数准备、签名、HTTP、读取和解码；父 context 更早截止时优先，精确 -1 不取消父时限或借用客户端的 Timeout。网络和连接池等待可取消，同步编码/解码或不配合 context 的钩子不能被强制中断，会在返回后检查取消状态。ctx 必须非 nil；无效 ctx 只在错误日志中使用 Background，不发起请求。

BaseURL 使用标准库 ResolveReference：`https://example.com/v1/` + `orders` 得到 `/v1/orders`，`/orders` 得到根路径。允许相对路径及同源绝对地址，拒绝跨源地址、userinfo、fragment 和 `//host/path`；不自动跟随重定向。
