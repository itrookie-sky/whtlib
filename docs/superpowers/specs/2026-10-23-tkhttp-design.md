# tkhttp HTTP 客户端设计

## 需求与状态

在 `toolkit/tkhttp` 提供框架无关、可并发复用的 HTTP 客户端。基本参数通过 `Config` 传入，单次请求参数通过 `RequestConfig` 传入，函数选项仅用于可选依赖注入。

用户已确认：在现有 `adapter` 包声明 LogAcc 契约，通过依赖注入供 tkhttp 使用，`logx` 提供实现；每次 HTTP 逻辑调用独立累计，包含重试，结束打印一条汇总日志。连接池基于标准库，通过函数选项注入，未注入时创建默认连接池。基本参数使用 Config，有合理默认值的补齐默认值，无法默认的必填参数缺失时直接报错。其他默认值和以下 API 均为本设计的建议，尚未实现。

本次交付设计文档。后续实现以确认后的设计为准，在当前仓库的 `main` 分支完成。

## 方案比较

| 方案 | 收益 | 代价与结论 |
| --- | --- | --- |
| 标准库 `net/http` + 现有 `LogAcc` | 复用连接池、TLS、代理、context 和日志；无新增第三方依赖 | 需要自行组织请求配置、重试和结果处理。推荐采用 |
| 引入第三方 HTTP 客户端再包装 | 可直接获得部分便捷请求和重试能力 | 增加依赖，仍需统一重试边界与 `LogAcc` 日志；目前需求不足以支持该成本 |
| 仅实现 `RoundTripper` 中间件 | 方便接入已有原生 HTTP 调用 | 无法覆盖调用方后续读取和解码失败，不适合承担整次调用的汇总日志 |

采用第一种方案。沿用仓库已有的 `adapter/` 命名，在其中声明通用 `LogAcc` 接口与工厂类型，由 `logx` 提供适配实现，`tkhttp` 仅依赖 `adapter`，通过工厂注入累计日志能力。现有 `adapter.Logger` 的普通参数采用正文拼接语义，保持其接口和字段类型不变，结构化累计由独立的 LogAcc 契约承担。

## 首版范围

- 复用 `http.Client` / `http.Transport`；通过 `WithTransport` 注入标准库连接池，未注入时创建默认池，也支持注入现有客户端。
- 支持 BaseURL、默认请求头、query、JSON、表单和原始字节请求体。
- 提供缓冲响应、可选 JSON 解码、明确的状态码错误和响应大小限制。
- 支持总超时、有限重试、指数退避和随机抖动。
- 提供签名请求头钩子，由调用方组合 `tksign`。
- 注入 `adapter.LogAccFactory`，为每次调用创建独立 LogAcc，打印一条最终日志。

流式响应、SSE、WebSocket、multipart 文件上传、大文件下载、缓存、限流和熔断留待独立需求。重试策略先在 `tkhttp` 内部实现；出现第二个实际消费者时，再抽取公共 `tkretry`，避免预先创建仅有一个使用方的抽象。

## 公开 API

以下签名用于约定接口，均为拟定 API：

```go
func New(config Config, opts ...Option) (client *Client, err error)
func WithTransport(transport *http.Transport) (option Option)
func WithLogAccFactory(factory adapter.LogAccFactory) (option Option)

func (c *Client) Do(ctx context.Context, method, target string, config RequestConfig) (response *Response, err error)
func (c *Client) Get(ctx context.Context, target string, config RequestConfig) (response *Response, err error)
func (c *Client) Post(ctx context.Context, target string, config RequestConfig) (response *Response, err error)

func (c *Client) CloseIdleConnections()
```

`Do` 是唯一执行入口。`Get`、`Post` 仅指定方法，其他方法直接通过 `Do` 调用。

### 客户端基本配置

```go
type Config struct {
	BaseURL          string        // 必填的服务基础地址。
	DefaultHeader    http.Header   // 所有请求使用的默认请求头。
	Timeout          time.Duration // 整次调用的总超时；0 使用默认值，-1 不添加组件超时。
	Retry            RetryPolicy   // 重试策略，零值表示仅尝试一次。
	MaxResponseBytes int64         // 单次响应的读取上限；0 使用默认值。
	DisableLog       bool          // 是否关闭 HTTP 汇总日志，默认 false。
}
```

| 字段 | 默认值与校验 |
| --- | --- |
| `BaseURL` | 无默认值，必填；必须是包含主机的 HTTP(S) 绝对 URL，不接受 userinfo、query 和 fragment |
| `DefaultHeader` | nil 表示无默认请求头；非法字段名或值在 New 中返回配置错误 |
| `Timeout` | 0 补齐 10 秒；正数按指定总时限执行；精确 -1 不添加组件超时，其他负数无效 |
| `Retry` | 零值仅尝试一次；其余字段默认值和校验见重试策略 |
| `MaxResponseBytes` | 0 补齐 8 MiB；显式非零值必须为正数且加一不会溢出 |
| `DisableLog` | false，启用每次调用的一条汇总日志；true 关闭 |

`New` 统一复制配置、补齐默认值并验证所有参数。BaseURL 缺失、无效 URL、非法 Header、无效时长或限额等返回 `ErrInvalidConfig`，错误指出字段和原因，不猜测域名、不静默修正非法值。精确 -1 指原始 `time.Duration(-1)`，不是 `-time.Second`。默认值补齐后才选择和创建连接池，初始化失败不遗留自有资源。

Config 中不包含 Transport、HTTPClient、LogAccFactory 和 RequestHook 等运行期依赖，也不为这些依赖再提供同名配置字段；调用方可以用配置文件或普通结构体集中管理基本参数。日志默认开启，此时 New 必须同时获得有效的 LogAccFactory 注入；缺失时直接返回 `ErrInvalidConfig`。Config.DisableLog 为 true 时可省略工厂，不创建累加器。

### 可选依赖注入

| 选项 | 行为 |
| --- | --- |
| `WithTransport(*http.Transport)` | 直接借用指定的标准库连接池；显式 nil 无效；优先于注入客户端的 Transport |
| `WithHTTPClient(*http.Client)` | 借用 Transport、Jar 和 Timeout 等公开配置；显式 nil 无效；Transport 为 nil 且没有 WithTransport 时创建默认池 |
| `WithLogAccFactory(adapter.LogAccFactory)` | 日志开启时必填；显式 nil 无效；每次调用创建独立累加器，后端生命周期由调用方管理 |
| `WithRequestHook(RequestHook)` | 每次执行尝试前生成签名或鉴权请求头；nil 表示未配置 |

忽略 nil 选项，同类注入以后一次为准。配置在构造期间固定，初始化后不提供配置修改方法。Config 默认容器在 New 中复制，依赖实例按后文的借用约定持有。

### LogAcc 共享契约与实现适配

以下声明计划放在 `adapter/log_acc.go`，仅依赖标准库 `context` 和 `time`：

```go
// LogAcc 定义累计字段和结构化打印的共享契约。
type LogAcc interface {
	SetValue(key string, value any) (acc LogAcc)
	Update(values map[string]any) (acc LogAcc)
	Skip(n int) (acc LogAcc)
	SetErr(value *error) (acc LogAcc)
	Debug(ctx context.Context, message string, args ...any)
	Info(ctx context.Context, message string, args ...any)
	Warn(ctx context.Context, message string, args ...any)
	Error(ctx context.Context, message string, args ...any)
}

// LogAccFactory 为一次逻辑调用创建独立的累计器，自身应支持并发调用。
type LogAccFactory func(start time.Time) (acc LogAcc)
```

SetValue、Update、Skip 和 SetErr 返回同一逻辑累加器，保留链式语义；打印参数按 `key, value` 对合并，并以结构化字段输出。SetErr 保留现有错误字段累计约定，tkhttp 的日志仍只写入受控错误分类。工厂每次返回字段为空、业务额外 Skip 为零的独立实例，不复用上一请求的字段 map；start 供实现计算可读耗时使用，tkhttp 始终另行累计结构化 costMs。LogAcc 和工厂均不提供 Close，日志后端由注入方管理。

`extension/logx` 增加适配工厂：

```go
func NewLogAccFactory(logger *Logger) (factory adapter.LogAccFactory)
```

该工厂每次创建一个现有 `logx.LogAcc`，设置 `WithLogger(logger)` 与 `WithCost(start)`，再用包内私有薄包装实现 adapter.LogAcc；创建时一次性补偿包装器自身的打印栈层数，不在每次打印时累计追加 Skip。logger 为 nil 时由 logx 选择其全局实例。工厂仅创建请求字段状态，复用长期 Logger，不为每次请求创建或关闭日志后端。

现有 LogAcc 的链式方法返回 `*logx.LogAcc`，与返回 adapter.LogAcc 的接口签名并不相同，因此不能直接赋值为该接口；私有包装器转发调用并返回自身，现有公开方法和返回类型保持兼容。依赖方向为 `tkhttp → adapter`、`logx → adapter`，调用方负责组合工厂；tkhttp 不导入 logx，不持有具体 Logger，也不通过类型别名将具体实现放进 adapter。

不提供注入单个累加器实例的入口，避免多个并发请求或连续请求共享可变字段。省略工厂时仅在显式关闭日志的配置下允许初始化；tkhttp 不隐式选择某个具体日志实现。显式 nil 工厂在 New 中拒绝；工厂在 Do 中返回 nil 或 typed nil 时返回 `ErrLogAccFactory`，不执行请求钩子或网络操作，也不回退到隐藏 Logger。工厂契约无效时无法生成汇总日志，由调用方处理这一依赖错误；正常的一次输出保证以工厂返回有效独立累加器为前提。

### 单次请求配置

```go
type RequestConfig struct {
	Header     http.Header    // 覆盖本次请求的同名默认请求头。
	Query      url.Values     // 合并到目标 URL 的 query，同名键整体替换。
	JSON       any            // JSON 请求体，nil 表示未设置。
	Form       url.Values     // 表单请求体，nil 表示未设置。
	Body       []byte         // 原始字节请求体，nil 表示未设置。
	Result     any            // 可选 JSON 解码目标，必须是非 nil 指针。
	Timeout    time.Duration  // 0 继承 Client，正数覆盖，-1 不添加组件超时。
	Idempotent *bool          // nil 按方法判断，true 显式允许，false 禁止重试。
	LogFields  map[string]any // 本次调用的非敏感业务日志字段。
}
```

| 字段 | 默认值与校验 |
| --- | --- |
| `Header` | nil 不覆盖；显式同名字段覆盖默认请求头，非法 Header 在发送前报错 |
| `Query` | nil 不增加参数；同名键整体替换，保留多值 |
| `JSON` | nil 表示未设置；设置时编码一次，默认 Content-Type 为 `application/json` |
| `Form` | nil 表示未设置；非 nil 空集合也是显式表单体，默认 Content-Type 为 `application/x-www-form-urlencoded` |
| `Body` | nil 表示未设置；非 nil 空切片也是显式字节体，不推测 Content-Type |
| `Result` | nil 不解码；非指针或 typed nil 在发送前返回参数错误；只解码最终成功响应 |
| `Timeout` | 0 继承 Client；正数覆盖；精确 -1 不添加组件超时，其他负数无效 |
| `Idempotent` | nil 使用方法默认规则；true 显式允许重试，false 禁止重试；不自动创建业务幂等机制 |
| `LogFields` | nil 无业务字段；设置时放入 `biz`，与内置 HTTP 字段隔离 |

`RequestConfig{}` 可用于不需要额外参数的请求，不再提供与其字段重复的请求 With 选项。JSON、Form、Body 三者互斥，同时设置多个请求体直接返回 `ErrInvalidRequest`。需要显式发送 JSON `null` 时使用 `JSON: json.RawMessage("null")`；typed nil 的 JSON 输入仍按标准库编码为 null。客户端和请求级 Header 对自动 Content-Type 的显式设置优先。

请求 method、target 没有可推断的默认值，缺失或无效时在发送前直接返回 `ErrInvalidRequest`。请求 Header、Result、超时和请求体冲突也在发送前校验；JSON 编码失败属于 prepare 错误，不进入网络阶段。`Get`、`Post` 的方法由各自 API 明确指定。

请求 Header/query/form/map 容器、字节请求体和 Idempotent 指向的布尔值在 Do 入口复制；调用方不得在复制期间并发修改。业务字段的嵌套可变对象由调用方保证调用期间不被并发修改。JSON 输入在 Do 中只编码一次，调用方在编码结束前不得修改输入；同一个 Result 目标的并发访问由调用方管理。

`ctx` 必须非 nil，nil 直接返回 `ErrInvalidRequest`；仅错误日志使用 `context.Background()` 满足 LogAcc 的非 nil 输出约定，不为网络请求补造 context。已经取消的合法 context 返回对应错误，日志仍使用原 context 打印，不发起网络请求。

## URL、连接池与超时

### URL 与重定向

BaseURL 采用标准库 `url.URL.ResolveReference` 的语义：

| BaseURL | target | 结果 |
| --- | --- | --- |
| `https://api.example.com/v1/` | `orders` | `https://api.example.com/v1/orders` |
| `https://api.example.com/v1/` | `/orders` | `https://api.example.com/orders` |
| `https://api.example.com/v1/` | `https://api.example.com/orders` | 使用同源绝对地址 |

BaseURL 必须在 Config 中提供，不被当成路径字符串直接拼接，末尾 `/` 按标准库规则影响相对路径解析。target 可使用相对路径或同源绝对地址。拒绝 target 中的 userinfo、fragment 和 `//host/path` 形式；绝对 target 必须与 BaseURL 保持同源。按 scheme、大小写规范化的 hostname 和有效端口判断同源。

首版不自动跟随重定向：通过 `http.ErrUseLastResponse` 取得原始 3xx 响应，再按状态码错误返回。注入客户端的 `CheckRedirect` 不沿用，此覆盖行为写入 API 注释；签名、Header 和请求体不会隐式转发到另一个 URL。

### 标准库连接池与注入

连接池直接使用标准库的 `*http.Transport`，由其管理连接建立、keep-alive、空闲回收和并发连接复用。`tkhttp` 仅负责选择和持有 Transport，保留标准库连接管理语义。

```go
transport := &http.Transport{
	Proxy: http.ProxyFromEnvironment,
	DialContext: (&net.Dialer{
		Timeout:   30 * time.Second,
		KeepAlive: 30 * time.Second,
	}).DialContext,
	ForceAttemptHTTP2:     true,
	MaxIdleConns:          200,
	MaxIdleConnsPerHost:   20,
	MaxConnsPerHost:       100,
	IdleConnTimeout:       90 * time.Second,
	TLSHandshakeTimeout:   10 * time.Second,
	ExpectContinueTimeout: time.Second,
}

client, err := tkhttp.New(
	tkhttp.Config{BaseURL: "https://api.example.com/v1/"},
	tkhttp.WithTransport(transport),
	tkhttp.WithLogAccFactory(logx.NewLogAccFactory(nil)),
)
// 处理 err；可将同一个 transport 注入其他 Client，共享实际连接池。
```

上述代码中的池参数是调用方自定义示例。注入直接使用原始 Transport 指针，保留已有连接和跨客户端共享能力，不对注入池调用 `Clone`。Transport 的配置在使用前完成，投入使用后不并发修改公开配置字段。

构造时按以下顺序确定最终 Transport；选项的书写顺序不改变这一级别，同类选项仍以后一次为准：

| 输入 | 最终 Transport | 所有者 |
| --- | --- | --- |
| 提供 `WithTransport(pool)` | 使用 pool，即使同时提供 WithHTTPClient | 调用方 |
| 仅提供 `WithHTTPClient(client)`，其 Transport 非 nil | 使用传入客户端的 Transport，支持原生 RoundTripper 扩展 | 调用方 |
| 未提供上述 Transport，包括注入客户端的 Transport 为 nil | 创建独立的默认 `*http.Transport` | tkhttp Client |

显式 `WithTransport(nil)`、`WithHTTPClient(nil)` 或最终选中的 typed nil RoundTripper 返回 `ErrInvalidConfig`。只有确定需要默认池时才创建；明确注入时不额外创建闲置池。

默认池在 `New` 中创建一次，供该 Client 的所有请求和重试复用；连接由 Transport 按请求需要建立。不同 Client 默认拥有不同的池，需要共享时显式注入同一实例。默认创建逻辑使用 `http.Transport`、`net.Dialer` 和标准库代理处理，参数以当前项目 Go 1.26.3 的标准默认配置为基线，与进程级 `http.DefaultTransport` 的替换相互独立：

| 默认参数 | 值与含义 |
| --- | --- |
| `Proxy` | `http.ProxyFromEnvironment` |
| `DialContext` | `net.Dialer`，拨号 Timeout 30s、TCP KeepAlive 30s |
| `ForceAttemptHTTP2` | true，尝试协商 HTTP/2 |
| `MaxIdleConns` | 100，所有主机的空闲连接总上限 |
| `MaxIdleConnsPerHost` | 0，沿用标准库 `DefaultMaxIdleConnsPerHost`，当前为 2 |
| `MaxConnsPerHost` | 0，沿用标准库不限制单主机连接总数的语义 |
| `IdleConnTimeout` | 90s |
| `TLSHandshakeTimeout` | 10s |
| `ExpectContinueTimeout` | 1s |
| `DisableKeepAlives` | false，启用连接复用 |

空闲连接上限与连接总数分别由标准库对应字段控制。`MaxConnsPerHost` 限制连接数量，不等于业务请求并发数，HTTP/2 可在同一连接上复用多个请求。调用方需要定制时配置标准 Transport 后注入，不再额外定义同义的连接池配置类型。

`tkhttp` 始终新建私有 `http.Client`。注入客户端时只复制公开配置，再应用最终 Transport 和组件重定向规则，不修改传入客户端；Transport/Jar 是借用资源，应满足原生 HTTP 客户端的并发约定。

`CloseIdleConnections` 只作用于组件创建的 Transport，借用 Transport 时为空操作；不取消正在执行的请求。HTTP 响应体统一由组件读取并关闭，返回值不要求调用方 Close。累计日志工厂及其日志后端始终由注入方管理，tkhttp 不关闭日志后端。

共享池由调用方在合适的生命周期节点统一调用 `transport.CloseIdleConnections()`；单个 tkhttp Client 不清理其他使用方共享的空闲连接。该标准库方法只关闭当前空闲连接，Transport 后续仍可按需建立连接。

### 总超时

组件超时覆盖整次逻辑调用，包括准备、签名钩子、各次请求、读取和重试等待；父 context 的更早截止时间始终生效。借用 `http.Client.Timeout` 仍可提前终止某次请求。context 可以取消网络与等待；同步编码、解码及调用方钩子不能被强制抢占，前后检查 context，钩子应配合 context 及时返回。

## 响应与错误

```go
type Response struct {
	StatusCode    int         // 最后一次响应的 HTTP 状态码。
	Header        http.Header // 复制的最后一次响应头。
	Body          []byte      // 最后一次响应的缓冲内容，超限时仅保留限额内前缀。
	BodyTruncated bool        // 是否因超过响应限额而截断。
}
```

- 2xx 视为 HTTP 成功；3xx、4xx、5xx 返回 `StatusError`，仍保留响应状态、Header 和限额内的 Body。
- 使用 `io.LimitReader` 读取至上限加一字节判断超限，不仅依赖 Content-Length；限制作用于组件实际读取的数据，自动解压时为解压后的数据。
- 超限返回 `ErrResponseTooLarge`，停止重试和解码。读取失败保留已读取内容并返回错误。
- 每次响应体都关闭；正常缓冲响应读至 EOF，超限或读取失败立即关闭，不为复用连接无界读取剩余内容。
- 只在 RequestConfig.Result 非 nil 时对最终成功、完整读取的响应执行解码；采用 `encoding/json.Unmarshal`，拒绝尾随的第二个 JSON 值。
- HEAD、204 和 205 不进行 JSON 解码，目标保持原值；其他成功状态的空响应在要求解码时返回解码错误。
- 解码失败不重试，返回 `Response` 和错误。解码目标可能被标准库部分写入，调用方不得在错误时当作完整结果使用。
- 尚未收到任何最终响应时返回 nil Response；如果此前失败响应后再次执行发生传输错误，也不把旧响应冒充为最终响应，历史结果保留在日志 `attempts` 中。

提供 `ErrInvalidConfig`、`ErrInvalidRequest`、`ErrResponseTooLarge`、`ErrRequestHook`、`ErrLogAccFactory` 等可供 `errors.Is` 判断的哨兵错误。`RequestError` 标识 `prepare`、`hook`、`transport`、`read`、`decode` 阶段，并通过 `Unwrap` 保留原始错误，支持 `errors.Is/AsType` 检查 context、网络和 JSON 错误。`StatusError` 保留状态码，但 Error 文本不包含响应正文。

HTTP 状态错误与读取错误同时出现时，以读取错误为主，Response 仍保存状态码。错误的公开描述采用受控文本，日志使用阶段和错误分类，不直接打印底层可能包含完整 URL 的 `url.Error.Error()`。

## 重试策略

```go
type RetryPolicy struct {
	MaxAttempts int           // 最大尝试次数，包含首次请求。
	BaseDelay   time.Duration // 指数退避的初始上限。
	MaxDelay    time.Duration // 单次重试等待的最大时长。
	StatusCodes []int         // 允许重试的 HTTP 状态码，nil 使用默认集合。
}
```

- `MaxAttempts` 包含首次请求，默认 1；0 使用默认值，负数无效。
- `BaseDelay` 默认 200ms，`MaxDelay` 默认 2s；0 使用默认值，负数或 MaxDelay 小于 BaseDelay 无效。
- `StatusCodes` 为 nil 时使用 429、502、503、504；非 nil 空集合关闭状态码重试，非空集合仅允许配置 4xx/5xx。
- RequestConfig.Idempotent 为 nil 时，GET、HEAD、OPTIONS 默认允许重试；其他方法须显式设为 true 才允许重试。显式 false 可禁止本次调用重试。
- 添加 Idempotency-Key 请求头本身不自动开启重试；业务幂等键在各次尝试中保持一致，远端须真正实现幂等约定。
- JSON、表单和字节请求体在准备阶段固定为可重放字节，每次尝试创建新的请求和 Body。
- 无响应头时，仅对明确的瞬时网络错误重试，包括连接重置、暂时性 DNS 错误、总 context 未截止时的传输超时；证书验证、无效参数、钩子失败、响应体读取/超限、解码失败不重试。
- 收到可重试状态时，先限额读取并关闭响应体，再等待并重试；读取失败或超限时直接返回错误。
- 使用 full jitter，在 `[0, min(MaxDelay, BaseDelay × 2^n)]` 中选等待时间，指数计算饱和处理，避免溢出。
- 有效 Retry-After 支持秒数和 HTTP 日期，等待时间取其与退避时间的较大值；超过 MaxDelay 或剩余总时限则保留当前状态错误并停止重试，不提前发送请求。无效值忽略，过去日期按 0 处理。
- 等待使用可取消计时器，总 context 取消或截止后立即终止；最终通过错误链保留相应 context 错误。

组件的 attempt 统计以自身调用 `http.Client.Do` 的次数为准；标准库 Transport 可能在内部重试连接，不计为额外组件尝试，也不承诺与线上 TCP 请求次数完全相等。

## 签名与鉴权钩子

```go
type RequestInfo struct {
	Method  string      // 本次尝试的请求方法。
	URL     string      // 最终发送的 URL，包含 query，供签名使用。
	Header  http.Header // 请求头快照，不包含底层 Jar/Transport 自动添加的字段。
	Body    []byte      // 固定请求体的副本。
	Attempt int         // 本次尝试序号，从 1 开始。
}

type RequestHook func(ctx context.Context, info RequestInfo) (headers http.Header, err error)
```

钩子返回补充或覆盖的请求头及错误。完整 URL、Header 和 Body 仅交给调用方钩子，不交给日志累加器。

钩子在每次调用 `http.Client.Do` 前执行，因此重试可重新生成时间戳、nonce 和签名。返回的请求头应用后发送，禁止覆盖 Host、Content-Length、Transfer-Encoding、Trailer 等传输管理字段。输入快照的修改不影响实际请求，钩子只负责返回请求头。

`tkhttp` 不规定签名串拼接规则。平台组件根据协议选择方法、路径、query、Header 和固定 Body 字节，调用已有 `tksign` 完成签名；签名针对最终发送字节，不再重新序列化一次 JSON。

钩子错误在本次逻辑调用中直接返回并纳入最终日志，不自动重试。钩子不拥有累计器或日志后端，也不自行打印一次成功或失败日志。

## LogAcc 累计与最终打印

每次 `Do` 调用在入口记录起点，日志开启时通过注入工厂获取独立的 `adapter.LogAcc`，校验非 nil 后累计字段：

```go
acc := logAccFactory(start)
```

Config.DisableLog 为 true 时不调用工厂，也不输出 HTTP 日志。累计和打印仅操作接口，具体的字段锁、结构化编码和输出目标由实现提供。

整个调用只在统一出口打印一次。初始化失败发生在 `New` 中，由调用方处理；不属于 HTTP 调用日志。调用过程不额外打印重试、签名或读响应日志。

累计步骤：入口添加方法、脱敏 URL 和 `biz`；请求准备完成后补充请求字节数；每次尝试结束后累计状态、耗时、错误分类和重试等待；最终补充响应信息、总耗时和最终结果，再根据级别调用一次 `acc.Info/Warn/Error`。

### 日志字段

内置字段放入 `http`，业务字段放入 `biz`。不允许业务字段覆盖 `http`；ctx 的 traceId 等字段由注入实现按自身配置提取；采用 logx 工厂时继续复用其既有上下文字段配置。

| 字段 | 含义 |
| --- | --- |
| `http.method` | 请求方法 |
| `http.url` | 仅包含 scheme、host、path；参数校验前为空，不包含 userinfo、query、fragment |
| `http.statusCode` | 最后一次响应状态码，未收到最终响应时为 0 |
| `http.requestBytes` | 编码后的固定请求体长度 |
| `http.responseBytes` | 最后一次响应实际读取字节数，检测超限时可达到限额加一 |
| `http.costMs` | 整次调用总耗时，包含准备、请求、读取、等待和解码 |
| `http.attemptCount` | 组件调用 `http.Client.Do` 的次数；参数或首次钩子失败时为 0 |
| `http.retryCount` | `max(0, attemptCount - 1)`，只统计实际执行的额外 Do |
| `http.attempts` | 每次 Do 的序号、状态码、耗时、错误分类和实际重试等待毫秒数；耗时覆盖 Do 至读取/关闭结束，不含签名和等待；使用独立快照 |
| `http.phase` | 最终结束阶段：prepare、hook、transport、read、status、decode、complete |
| `http.errorKind` | 最终错误分类，无错误时为空；不包含原始请求/响应数据 |
| `http.retryStopReason` | 重试耗尽、方法不允许、超过等待上限或总预算不足等停止原因，未涉及时省略 |
| `biz` | 调用方显式传入的非敏感业务字段 |

logx 适配工厂通过其内部 `WithCost` 提供可读耗时前缀，其他实现可选择等价展示；`http.costMs` 始终由 tkhttp 累计，用于结构化检索。成功最终打印 Info；HTTP 3xx/4xx 和主动取消打印 Warn；5xx、总超时、准备、钩子、传输、读取和解码错误打印 Error。此前发生重试但最终成功时仍打印一条 Info，失败尝试保留在数组中。

默认仅记录上述元数据，不记录 query 值、请求/响应正文、请求/响应 Header、Cookie、鉴权值和签名；首版不提供直接开启完整报文日志的开关。自定义日志字段显式传入，不自动反射请求对象。

每个请求的累加器和 attempts 集合都独立，最终转成不会再修改的字段快照后输出。接口实现应同步保护自身字段 map，tkhttp 仍负责嵌套切片/map 的独立快照，不把 map 的锁当作嵌套对象的同步措施。通过接口 Skip 补偿固定打印层数，logx 的适配层也计算在内；Do 与便捷入口分别校准到业务调用处。

示例：第一次得到 503、第二次成功，只产生一条类似日志：

```text
[INFO] {trace-123} caller/service.go:42 [96ms] HTTP 请求完成 {"biz":{"action":"queryDevice"},"http":{"method":"GET","url":"https://api.example.com/v1/devices","statusCode":200,"requestBytes":0,"responseBytes":128,"costMs":96,"attemptCount":2,"retryCount":1,"attempts":[{"attempt":1,"statusCode":503,"costMs":14,"errorKind":"httpStatus","retryWaitMs":64},{"attempt":2,"statusCode":200,"costMs":18,"errorKind":"","retryWaitMs":0}],"phase":"complete","errorKind":""}}
```

取消后仍将合法的原 context 传给 LogAcc 接口，保留 trace 信息。打印方法不返回错误，输出失败的诊断由实现负责；采用 logx 时沿用其 stderr 诊断，不覆盖 HTTP 返回结果。进程崩溃、调用方或注入实现 panic、无效工厂和实际日志写入故障不属于“一次正常返回、一次输出调用”的保证。

## 使用示例

以下代码展示拟定用法，实际包尚未创建：

```go
func fetchDevices(ctx context.Context, logAccFactory adapter.LogAccFactory) (result map[string]any, err error) {
	client, err := tkhttp.New(
		tkhttp.Config{
			BaseURL: "https://api.example.com/v1/",
			Timeout: 5 * time.Second,
			Retry:   tkhttp.RetryPolicy{MaxAttempts: 3},
		},
		tkhttp.WithLogAccFactory(logAccFactory),
	)
	if err != nil {
		return
	}
	defer client.CloseIdleConnections()

	_, err = client.Get(ctx, "devices", tkhttp.RequestConfig{
		Query:     url.Values{"page": {"1"}},
		Result:    &result,
		LogFields: map[string]any{"action": "queryDevice"},
	})
	return
}
```

调用方可先通过 `logx.NewLogAccFactory(logger)` 取得适配工厂，再传入上述函数；也可注入其他满足 adapter 契约的实现。示例将创建、调用和释放放在一个函数中便于阅读。实际服务在启动时创建并共享 Client，退出时清理自有空闲连接；每次调用只传入 context 和 RequestConfig。只需要 HTTP 而不记录日志时，可使用 `Config{BaseURL: baseURL, DisableLog: true}` 省略工厂。

## 文件组织与验证建议

建议在 `toolkit/tkhttp/` 按 `client.go`、`config.go`、`options.go`、`request.go`、`response.go`、`retry.go`、`hook.go`、`log.go`、`error.go` 和 `README.md` 组织。config.go 负责基本参数、默认值和校验，options.go 仅负责可选依赖注入。`adapter/log_acc.go` 声明 logx 与 tkhttp 实际共享的契约；`extension/logx/log_acc_adapter.go` 提供私有包装与公开工厂。仅供 tkhttp 使用的其他类型和辅助逻辑留在组件内部。

实现后应检查成功、准备失败、超时取消、HTTP 状态错误、读取超限、解码失败和重试耗尽等路径；特别验证两次尝试最终只输出一条日志、并发请求字段隔离、签名字节与发送字节一致、敏感数据不进入日志，以及借用客户端和连接池的配置不被修改、借用池不被主动清理、注入的日志后端不被关闭。

连接池验证还应覆盖：未注入时创建独立默认池；单 Client 多次请求和重试复用同一 Transport；多个 Client 注入同一池时保留实例身份和连接共享；WithTransport 与 WithHTTPClient 的组合顺序不改变优先级；注入客户端的 nil Transport 触发默认创建；显式 nil 和 typed nil 拒绝；组件仅清理自有池的空闲连接。实际连接复用通过本地 httptest 的连接状态或 httptrace 验证。

配置验证还应覆盖：填写 BaseURL 并注入有效日志工厂时补齐合理默认值，关闭日志时可省略工厂；BaseURL 缺失直接报错；非法时长、限额、Header 和重试策略在 New 中拒绝；RequestConfig 零值、超时继承/禁用、typed nil Result、JSON null 和互斥请求体的行为一致；nil context 返回参数错误且能完成错误日志；所有参数错误都不发起网络请求。

日志契约验证还应覆盖：开启日志但缺失工厂、显式 nil 工厂在 New 中拒绝；工厂返回 nil/typed nil 在网络前报错；关闭日志不调用工厂；并发请求与重试的工厂调用次数正确，每个逻辑请求只创建一个累计器；注入非 logx 实现仍可累计和打印；logx 包装器满足接口、链式调用及源文件位置正确、旧 LogAcc API 保持兼容。包依赖应检查 tkhttp 的日志依赖只有 adapter，不导入 logx。

按全局工作流，新增测试需要另行获得用户允许。获准后仅采用 `httptest` 或自定义 RoundTripper，覆盖取消/等待、Retry-After、body 重放、URL 解析、资源关闭、日志数量和调用位置，并对受影响的 `adapter`、`logx` 和 `tkhttp` 包运行 race 与 vet 检查，不连接真实平台或运行整个仓库集成测试。本次仅修改设计文档，不编写或执行新增测试。

## 参考

- [Go net/http：并发复用 Client/Transport、响应体关闭、context 和重定向](https://pkg.go.dev/net/http)
- [Go http.Transport：连接池参数、Clone 与空闲连接回收](https://pkg.go.dev/net/http#Transport)
- [Go net/url：ResolveReference 的相对地址规则](https://pkg.go.dev/net/url#URL.ResolveReference)
- [Go io：LimitReader 与有限读取](https://pkg.go.dev/io#LimitReader)
- [AWS Builders’ Library：超时、重试、退避与随机抖动](https://aws.amazon.com/builders-library/timeouts-retries-and-backoff-with-jitter/)
- [现有 LogAcc 使用约定](../../../extension/logx/README.md#logacc-累积日志字段)
