# tkhttp HTTP 客户端设计

## 需求与状态

在 `toolkit/tkhttp` 提供框架无关、可并发复用的 HTTP 客户端。基本参数通过 `Config` 传入，单次请求通过可省略的 `RequestConfig` 覆盖配置，省略时使用 Client 默认值，函数选项仅用于可选依赖注入。

用户已确认：在现有 `adapter` 包声明 LogAcc 契约，通过依赖注入供 tkhttp 使用，`logx` 提供实现；每次 HTTP 调用独立累计，结束打印一条汇总日志。连接池基于标准库，通过函数选项注入，未注入时创建默认连接池。基本参数使用 Config，有合理默认值的补齐默认值，无法默认的必填参数缺失时直接报错。请求接口中的配置参数可省略，未传时使用 Client 默认配置。LogAcc 的开始时间支持构造时传入和通过 SetStartTime 修改，通过 tkhttp.LogAccFactory 为每次调用创建新累加器，LogAcc 不提供 Clone。tkhttp 不提供重试能力，也不依赖 tkretry；需要重试时由调用方显式组合独立组件。以下 API 和默认约定已在首版实现。

已按本设计在 `main` 分支实现 tkhttp、共享 LogAcc 契约和 logx 适配层；用法见 [tkhttp README](../../../toolkit/tkhttp/README.md)。tkretry 仍为独立设计，本次未实现。

## 方案比较


| 方案                              | 收益                                                    | 代价与结论                                                           |
| ----------------------------------- | --------------------------------------------------------- | ---------------------------------------------------------------------- |
| 标准库 `net/http` + 现有 `LogAcc` | 复用连接池、TLS、代理、context 和日志；无新增第三方依赖 | 需要自行组织请求配置和结果处理。推荐采用                             |
| 引入第三方 HTTP 客户端再包装      | 可直接获得部分便捷请求和重试能力                        | 增加依赖，仍需统一请求行为与 `LogAcc` 日志；目前需求不足以支持该成本 |
| 仅实现 `RoundTripper` 中间件      | 方便接入已有原生 HTTP 调用                              | 无法覆盖调用方后续读取和解码失败，不适合承担整次调用的汇总日志       |

采用第一种方案。沿用仓库已有的 `adapter/` 命名，在其中声明通用 `LogAcc` 接口，由 `logx` 提供适配实现，`tkhttp` 通过本包的 LogAccFactory 注入累计日志能力。现有 `adapter.Logger` 的普通参数采用正文拼接语义，保持其接口和字段类型不变，结构化累计由独立的 LogAcc 契约承担。

## 首版范围

- 复用 `http.Client` / `http.Transport`；通过 `WithTransport` 注入标准库连接池，未注入时创建默认池，也支持注入现有客户端。
- 支持 BaseURL、默认请求头、query、JSON、表单和原始字节请求体。
- 提供缓冲响应、可选 JSON 解码、明确的状态码错误和响应大小限制。
- 支持本次调用的总超时和 context 取消；网络阶段只调用一次 http.Client.Do。
- 提供签名请求头钩子，由调用方组合 `tksign`。
- 注入 LogAccFactory，每次调用创建新的 adapter.LogAcc 并设置开始时间，打印一条最终日志。

流式响应、SSE、WebSocket、multipart 文件上传、大文件下载、缓存、限流和熔断留待独立需求。tkretry 是独立组件，其方案保存在单独文档中，tkhttp 的配置、执行流程和日志不包含重试策略或统计。

## 公开 API

公开 API 如下：

```go
func New(config Config, opts ...Option) (client *Client, err error)
func WithTransport(transport *http.Transport) (option Option)
type LogAccFactory func() (acc adapter.LogAcc)

func WithLogAcc(factory LogAccFactory) (option Option)

func (c *Client) Do(ctx context.Context, method, target string, configs ...RequestConfig) (response *Response, err error)
func (c *Client) Get(ctx context.Context, target string, configs ...RequestConfig) (response *Response, err error)
func (c *Client) Post(ctx context.Context, target string, configs ...RequestConfig) (response *Response, err error)

func (c *Client) CloseIdleConnections()
```

`Do` 是唯一执行入口。`Get`、`Post` 仅指定方法并透传可选配置，其他方法直接通过 `Do` 调用。请求配置使用值类型可变参数，允许传入 0 或 1 份；超过 1 份返回 `ErrInvalidRequest`，不执行请求钩子或网络操作。New 的客户端 Config 仍为必填，用于建立可继承的默认配置。

### 客户端基本配置

```go
type Config struct {
	BaseURL          string        // 必填的服务基础地址。
	DefaultHeader    http.Header   // 所有请求使用的默认请求头。
	Timeout          time.Duration // 整次调用的总超时；0 使用默认值，-1 不添加组件超时。
	MaxResponseBytes int64         // 单次响应的读取上限；0 使用默认值。
	DisableLog       bool          // 是否关闭 HTTP 汇总日志，默认 false。
}
```


| 字段               | 默认值与校验                                                                          |
| -------------------- | --------------------------------------------------------------------------------------- |
| `BaseURL`          | 无默认值，必填；必须是包含主机的 HTTP(S) 绝对 URL，不接受 userinfo、query 和 fragment |
| `DefaultHeader`    | nil 表示无默认请求头；非法字段名或值在 New 中返回配置错误                             |
| `Timeout`          | 0 补齐 10 秒；正数按指定总时限执行；精确 -1 不添加组件超时，其他负数无效              |
| `MaxResponseBytes` | 0 补齐 8 MiB；显式非零值必须为正数且加一不会溢出                                      |
| `DisableLog`       | false，启用每次调用的一条汇总日志；true 关闭                                          |

`New` 统一复制配置、补齐默认值并验证所有参数。BaseURL 缺失、无效 URL、非法 Header、无效时长或限额等返回 `ErrInvalidConfig`，错误指出字段和原因，不猜测域名、不静默修正非法值。精确 -1 指原始 `time.Duration(-1)`，不是 `-time.Second`。默认值补齐后才选择和创建连接池，初始化失败不遗留自有资源。

Config 中不包含 Transport、HTTPClient、LogAccFactory 和 RequestHook 等运行期依赖，也不为这些依赖再提供同名配置字段。日志默认开启，此时 New 必须获得非 nil 的 LogAccFactory；缺失时返回 `ErrInvalidConfig`。Config.DisableLog 为 true 时可省略工厂，不创建累加器。

### 可选依赖注入


| 选项                             | 行为                                                                                                         |
| ---------------------------------- | -------------------------------------------------------------------------------------------------------------- |
| `WithTransport(*http.Transport)` | 直接借用指定的标准库连接池；显式 nil 无效；优先于注入客户端的 Transport                                      |
| `WithHTTPClient(*http.Client)`   | 借用 Transport、Jar 和 Timeout 等公开配置；显式 nil 无效；Transport 为 nil 且没有 WithTransport 时创建默认池 |
| `WithLogAcc(LogAccFactory)` | 日志开启时必填；显式 nil 无效；每次调用创建新实例，后端生命周期由调用方管理 |
| `WithRequestHook(RequestHook)`   | 本次请求发送前生成签名或鉴权请求头；nil 表示未配置                                                           |

忽略 nil 选项，同类注入以后一次为准。配置在构造期间固定，初始化后不提供配置修改方法。Config 默认容器在 New 中复制，依赖实例按后文的借用约定持有。

### LogAcc 共享契约与实现适配

以下共享声明位于 `adapter/log_acc.go`，仅依赖标准库 `context` 和 `time`：

```go
// LogAcc 定义累计字段和结构化打印的共享契约。
type LogAcc interface {
	SetStartTime(start time.Time) (acc LogAcc)
	SetValue(key string, value any) (acc LogAcc)
	Update(values map[string]any) (acc LogAcc)
	Skip(n int) (acc LogAcc)
	SetErr(value *error) (acc LogAcc)
	Debug(ctx context.Context, message string, args ...any)
	Info(ctx context.Context, message string, args ...any)
	Warn(ctx context.Context, message string, args ...any)
	Error(ctx context.Context, message string, args ...any)
}
```

SetStartTime、SetValue、Update、Skip 和 SetErr 返回同一逻辑累加器，保留链式语义；打印参数按 `key, value` 对合并，并以结构化字段输出。SetErr 保留现有错误字段累计约定，tkhttp 的日志仍只写入受控错误分类。接口不提供 Close，日志后端由注入方管理。

开始时间有两个设置入口：构造时沿用现有 `logx.NewLogAcc(logx.WithCost(start))`，创建后通过 `SetStartTime(start)` 随时修改。零时间关闭可读耗时前缀；非零时间作为后续打印的计时起点。SetStartTime 与字段快照使用同一同步机制，保证修改和打印可并发执行，不只在构造阶段直接写字段。

LogAcc 接口和具体实现均不提供 Clone。每次调用由 LogAccFactory 创建新的累加器，字段 map、计时起点和 Skip 独立，日志后端可共享。字段值中的指针、切片和 map 仍由调用方保证同步，禁止按值复制含 mutex 的累加器。

`extension/logx` 保留原 NewLogAcc 和链式返回类型，并补充以下 API：

```go
func (a *LogAcc) SetStartTime(start time.Time) (acc *LogAcc)
func WithLogger(logger adapter.Logger) (op LogAccOptionFunc)
func NewLoggerAdapter(logger *Logger) (result adapter.Logger)
func NewLogAccAdapter(opts ...LogAccOptionFunc) (acc adapter.LogAcc)
```

NewLogAccAdapter 直接创建一个累计器及其包内私有薄包装，调用方可以在创建时传 WithLogger、WithCost，也可以通过返回接口的 SetStartTime 再修改起点。WithLogger 接收 adapter.Logger，支持 GoFrame 等后端；LogAcc 负责累计字段 JSON 和耗时正文，后端负责级别、context 和实际写入。不传 Logger、传 nil 或 typed nil 时使用全局 logx Logger 的适配器，后端仍由调用方管理。使用 logx 实例时通过 NewLoggerAdapter 转为共享契约，Skip 只借用后端，不增加文件引用。包装器转发链式操作并返回自身，构造时仅补偿自身一层调用栈。现有具体 LogAcc 的链式方法返回 `*logx.LogAcc`，不能直接满足返回 adapter.LogAcc 的接口，因此保留薄适配层。

```go
acc := logx.NewLogAccAdapter(logx.WithCost(time.Now()))
acc.SetStartTime(time.Now())
```

依赖方向为 `tkhttp → adapter`、`logx → adapter`。LogAccFactory 仅供 tkhttp 使用，定义在 tkhttp 包；调用方负责组合构造逻辑，tkhttp 不导入 logx，不持有具体 Logger。共享 LogAcc 接口只描述累计器行为。

日志开启时，New 只校验并保存非 nil 工厂，不执行工厂。每次 Do 调用一次工厂获得新实例，校验结果后调用 SetStartTime 设置本次调用的入口时间。工厂必须支持并发调用并返回独立的字段、起点和 Skip 状态，不能复用同一累加器。`http` 与 `biz` 保留字段由本次调用完整覆盖。

省略工厂时仅在显式关闭日志的配置下允许初始化。显式 nil 工厂在 New 中返回 `ErrInvalidConfig`。Do 中工厂返回 nil 或 typed nil 时返回 `ErrLogAcc`，不执行请求钩子或网络操作，也不回退到隐藏 Logger；此时没有有效累加器可打印。关闭日志时不调用工厂或 SetStartTime。

### 单次请求配置

不传 RequestConfig 时，直接使用 Client 初始化后已归一化的默认请求头、超时、响应限额、日志开关及已注入依赖；传入零值 `RequestConfig{}` 与省略参数等价。query、正文、解码目标和业务日志字段默认均为空，不沿用上一次请求的数据。

传入一份配置时，先创建本次调用的有效配置：Header 按字段覆盖 Client 默认请求头，Timeout 为 0 时继承 Client，正数覆盖，精确 -1 不添加组件超时；其余请求字段按下表处理。合并只影响当前请求，复制所需容器，不修改 Client 的默认值、传入配置或其他请求。Client 的响应限额、日志开关和依赖继续复用，不在 RequestConfig 中重复声明。

```go
type RequestConfig struct {
	Header    http.Header    // 覆盖本次请求的同名默认请求头。
	Query     url.Values     // 合并到目标 URL 的 query，同名键整体替换。
	JSON      any            // JSON 请求体，nil 表示未设置。
	Form      url.Values     // 表单请求体，nil 表示未设置。
	Body      []byte         // 原始字节请求体，nil 表示未设置。
	Result    any            // 可选 JSON 解码目标，必须是非 nil 指针。
	Timeout   time.Duration  // 0 继承 Client，正数覆盖，-1 不添加组件超时。
	LogFields map[string]any // 本次调用的非敏感业务日志字段。
}
```


| 字段        | 默认值与校验                                                                                          |
| ------------- | ------------------------------------------------------------------------------------------------------- |
| `Header`    | nil 不覆盖；显式同名字段覆盖默认请求头，非法 Header 在发送前报错                                      |
| `Query`     | nil 不增加参数；同名键整体替换，保留多值                                                              |
| `JSON`      | nil 表示未设置；设置时编码一次，默认 Content-Type 为 `application/json`                               |
| `Form`      | nil 表示未设置；非 nil 空集合也是显式表单体，默认 Content-Type 为 `application/x-www-form-urlencoded` |
| `Body`      | nil 表示未设置；非 nil 空切片也是显式字节体，不推测 Content-Type                                      |
| `Result`    | nil 不解码；非指针或 typed nil 在发送前返回参数错误；只解码本次成功响应                               |
| `Timeout`   | 0 继承 Client；正数覆盖；精确 -1 不添加组件超时，其他负数无效                                         |
| `LogFields` | nil 无业务字段；设置时放入 `biz`，与内置 HTTP 字段隔离                                                |

不需要额外参数时直接省略 RequestConfig；也可显式传入 `RequestConfig{}`，效果相同。不再提供与其字段重复的请求 With 选项。JSON、Form、Body 三者互斥，同时设置多个请求体直接返回 `ErrInvalidRequest`。需要显式发送 JSON `null` 时使用 `JSON: json.RawMessage("null")`；typed nil 的 JSON 输入仍按标准库编码为 null。客户端和请求级 Header 对自动 Content-Type 的显式设置优先。

请求 method、target 没有可推断的默认值，缺失或无效时在发送前直接返回 `ErrInvalidRequest`。请求 Header、Result、超时和请求体冲突也在发送前校验；JSON 编码失败属于 prepare 错误，不进入网络阶段。`Get`、`Post` 的方法由各自 API 明确指定。

可选配置数量在 Do 入口校验，最多一份；请求 Header/query/form/map 容器和字节请求体在选择有效配置后复制；调用方不得在复制期间并发修改。业务字段的嵌套可变对象由调用方保证调用期间不被并发修改。JSON 输入在 Do 中只编码一次，调用方在编码结束前不得修改输入；同一个 Result 目标的并发访问由调用方管理。

`ctx` 必须非 nil，nil 直接返回 `ErrInvalidRequest`；仅错误日志使用 `context.Background()` 满足 LogAcc 的非 nil 输出约定，不为网络请求补造 context。已经取消的合法 context 返回对应错误，日志仍使用原 context 打印，不发起网络请求。

## URL、连接池与超时

### URL 与重定向

BaseURL 采用标准库 `url.URL.ResolveReference` 的语义：


| BaseURL                       | target                           | 结果                                |
| ------------------------------- | ---------------------------------- | ------------------------------------- |
| `https://api.example.com/v1/` | `orders`                         | `https://api.example.com/v1/orders` |
| `https://api.example.com/v1/` | `/orders`                        | `https://api.example.com/orders`    |
| `https://api.example.com/v1/` | `https://api.example.com/orders` | 使用同源绝对地址                    |

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
	tkhttp.WithLogAcc(func() (acc adapter.LogAcc) {
		return logx.NewLogAccAdapter()
	}),
)
// 处理 err；可将同一个 transport 注入其他 Client，共享实际连接池。
```

上述代码中的池参数是调用方自定义示例。注入直接使用原始 Transport 指针，保留已有连接和跨客户端共享能力，不对注入池调用 `Clone`。Transport 的配置在使用前完成，投入使用后不并发修改公开配置字段。

构造时按以下顺序确定最终 Transport；选项的书写顺序不改变这一级别，同类选项仍以后一次为准：


| 输入                                                    | 最终 Transport                                         | 所有者        |
| --------------------------------------------------------- | -------------------------------------------------------- | --------------- |
| 提供 `WithTransport(pool)`                              | 使用 pool，即使同时提供 WithHTTPClient                 | 调用方        |
| 仅提供 `WithHTTPClient(client)`，其 Transport 非 nil    | 使用传入客户端的 Transport，支持原生 RoundTripper 扩展 | 调用方        |
| 未提供上述 Transport，包括注入客户端的 Transport 为 nil | 创建独立的默认 `*http.Transport`                       | tkhttp Client |

显式 `WithTransport(nil)`、`WithHTTPClient(nil)` 或最终选中的 typed nil RoundTripper 返回 `ErrInvalidConfig`。只有确定需要默认池时才创建；明确注入时不额外创建闲置池。

默认池在 `New` 中创建一次，供该 Client 的所有请求复用；连接由 Transport 按请求需要建立。不同 Client 默认拥有不同的池，需要共享时显式注入同一实例。默认创建逻辑使用 `http.Transport`、`net.Dialer` 和标准库代理处理，参数以当前项目 Go 1.26.3 的标准默认配置为基线，与进程级 `http.DefaultTransport` 的替换相互独立：


| 默认参数                | 值与含义                                             |
| ------------------------- | ------------------------------------------------------ |
| `Proxy`                 | `http.ProxyFromEnvironment`                          |
| `DialContext`           | `net.Dialer`，拨号 Timeout 30s、TCP KeepAlive 30s    |
| `ForceAttemptHTTP2`     | true，尝试协商 HTTP/2                                |
| `MaxIdleConns`          | 100，所有主机的空闲连接总上限                        |
| `MaxIdleConnsPerHost`   | 0，沿用标准库 `DefaultMaxIdleConnsPerHost`，当前为 2 |
| `MaxConnsPerHost`       | 0，沿用标准库不限制单主机连接总数的语义              |
| `IdleConnTimeout`       | 90s                                                  |
| `TLSHandshakeTimeout`   | 10s                                                  |
| `ExpectContinueTimeout` | 1s                                                   |
| `DisableKeepAlives`     | false，启用连接复用                                  |

空闲连接上限与连接总数分别由标准库对应字段控制。`MaxConnsPerHost` 限制连接数量，不等于业务请求并发数，HTTP/2 可在同一连接上复用多个请求。调用方需要定制时配置标准 Transport 后注入，不再额外定义同义的连接池配置类型。

`tkhttp` 始终新建私有 `http.Client`。注入客户端时只复制公开配置，再应用最终 Transport 和组件重定向规则，不修改传入客户端；Transport/Jar 是借用资源，应满足原生 HTTP 客户端的并发约定。

`CloseIdleConnections` 只作用于组件创建的 Transport，借用 Transport 时为空操作；不取消正在执行的请求。HTTP 响应体统一由组件读取并关闭，返回值不要求调用方 Close。日志后端始终由调用方管理，tkhttp 仅维护每次调用新建的累计状态，不关闭日志后端。

共享池由调用方在合适的生命周期节点统一调用 `transport.CloseIdleConnections()`；单个 tkhttp Client 不清理其他使用方共享的空闲连接。该标准库方法只关闭当前空闲连接，Transport 后续仍可按需建立连接。

### 总超时

组件超时覆盖本次调用，包括准备、签名钩子、本次请求、读取和解码；父 context 的更早截止时间始终生效。借用 `http.Client.Timeout` 仍可提前终止某次请求。context 可以取消网络与连接池等待；同步编码、解码及调用方钩子不能被强制抢占，前后检查 context，钩子应配合 context 及时返回。

## 响应与错误

```go
type Response struct {
	StatusCode    int         // 本次响应的 HTTP 状态码。
	Header        http.Header // 复制的本次响应头。
	Body          []byte      // 本次响应的缓冲内容，超限时仅保留限额内前缀。
	BodyTruncated bool        // 是否因超过响应限额而截断。
}
```

- 2xx 视为 HTTP 成功；3xx、4xx、5xx 返回 `StatusError`，仍保留响应状态、Header 和限额内的 Body。
- 使用 `io.LimitReader` 读取至上限加一字节判断超限，不仅依赖 Content-Length；限制作用于组件实际读取的数据，自动解压时为解压后的数据。
- 超限返回 `ErrResponseTooLarge`，停止读取和解码。读取失败保留已读取内容并返回错误。
- 每次响应体都关闭；正常缓冲响应读至 EOF，超限或读取失败立即关闭，不为复用连接无界读取剩余内容。
- 只在 RequestConfig.Result 非 nil 时对本次成功、完整读取的响应执行解码；采用 `encoding/json.Unmarshal`，拒绝尾随的第二个 JSON 值。
- HEAD、204 和 205 不进行 JSON 解码，目标保持原值；其他成功状态的空响应在要求解码时返回解码错误。
- 解码失败返回 `Response` 和错误。解码目标可能被标准库部分写入，调用方不得在错误时当作完整结果使用。
- 未收到响应时返回 nil Response；响应头已收到时，读取或解码失败仍返回本次 Response 及错误。

提供 `ErrInvalidConfig`、`ErrInvalidRequest`、`ErrResponseTooLarge`、`ErrRequestHook`、`ErrLogAcc` 等可供 `errors.Is` 判断的哨兵错误。`RequestError` 标识 `prepare`、`hook`、`transport`、`read`、`decode` 阶段，并通过 `Unwrap` 保留原始错误，支持 `errors.Is/AsType` 检查 context、网络和 JSON 错误。`StatusError` 保留状态码，但 Error 文本不包含响应正文。

HTTP 状态错误与读取错误同时出现时，以读取错误为主，Response 仍保存状态码。错误的公开描述采用受控文本，日志使用阶段和错误分类，不直接打印底层可能包含完整 URL 的 `url.Error.Error()`。

## 单次调用边界

每次 Do 只负责一个 HTTP 调用，进入网络阶段后仅调用一次 http.Client.Do，Transport 自身的连接处理沿用标准库语义。tkhttp 不选择重试状态码、不解析 Retry-After、不判断业务幂等性，也不持有重试执行器。

调用方需要重试时，可在业务层显式组合独立的 [tkretry 设计](2026-10-23-tkretry-design.md)。每次调用 tkhttp 都有独立的配置、计时和汇总日志；跨调用的尝试次数、等待与最终业务结果由外层管理。Config.Timeout 约束本次调用，不包含外部多次调用或等待；共同的业务总时限由调用方 context 控制。

## 签名与鉴权钩子

```go
type RequestInfo struct {
	Method string      // 本次请求的方法。
	URL    string      // 最终发送的 URL，包含 query，供签名使用。
	Header http.Header // 请求头快照，不包含底层 Jar/Transport 自动添加的字段。
	Body   []byte      // 固定请求体的副本。
}

type RequestHook func(ctx context.Context, info RequestInfo) (headers http.Header, err error)
```

钩子返回补充或覆盖的请求头及错误。完整 URL、Header 和 Body 仅交给调用方钩子，不交给日志累加器。

钩子在本次调用 `http.Client.Do` 前执行一次，用于生成时间戳、nonce 和签名。返回的请求头应用后发送，禁止覆盖 Host、Content-Length、Transfer-Encoding、Trailer 等传输管理字段。输入快照的修改不影响实际请求，钩子只负责返回请求头。

`tkhttp` 不规定签名串拼接规则。平台组件根据协议选择方法、路径、query、Header 和固定 Body 字节，调用已有 `tksign` 完成签名；签名针对最终发送字节，不再重新序列化一次 JSON。

钩子错误在本次调用中直接返回并纳入最终日志。钩子不拥有累计器或日志后端，也不自行打印一次成功或失败日志。

## LogAcc 累计与最终打印

每次 `Do` 调用在入口记录起点，日志开启时通过 LogAccFactory 创建独立的 `adapter.LogAcc`，校验非 nil 后设置开始时间并累计字段：

```go
acc := logAccFactory()
acc.SetStartTime(start)
```

Config.DisableLog 为 true 时不调用工厂，也不输出 HTTP 日志。累计和打印仅操作接口，具体的字段锁、结构化编码和输出目标由实现提供。

整个调用只在统一出口打印一次。初始化失败发生在 `New` 中，由调用方处理；不属于 HTTP 调用日志。调用过程不额外打印签名或读响应日志。

累计步骤：入口添加方法、脱敏 URL 和 `biz`；请求准备完成后补充请求字节数；响应处理结束后补充状态、响应字节数、总耗时、错误阶段与分类，再根据级别调用一次 `acc.Info/Warn/Error`。

### 日志字段

内置字段放入 `http`，业务字段放入 `biz`。不允许业务字段覆盖 `http`；ctx 的 traceId 等字段由注入实现按自身配置提取；采用 logx 适配实例时继续复用其既有上下文字段配置。


| 字段                 | 含义                                                                        |
| ---------------------- | ----------------------------------------------------------------------------- |
| `http.method`        | 请求方法                                                                    |
| `http.url`           | 仅包含 scheme、host、path；参数校验前为空，不包含 userinfo、query、fragment |
| `http.statusCode`    | 本次响应状态码，未收到响应时为 0                                            |
| `http.requestBytes`  | 编码后的固定请求体长度                                                      |
| `http.responseBytes` | 本次响应实际读取字节数，检测超限时可达到限额加一                            |
| `http.costMs`        | 整次调用总耗时，包含准备、签名、请求、读取和解码                            |
| `http.phase`         | 最终结束阶段：prepare、hook、transport、read、status、decode、complete      |
| `http.errorKind`     | 最终错误分类，无错误时为空；不包含原始请求/响应数据                         |
| `biz`                | 调用方显式传入的非敏感业务字段                                              |

logx 适配实现通过当前起点提供可读耗时前缀，起点可在构造时传入或通过 SetStartTime 修改；其他实现可选择等价展示；`http.costMs` 始终由 tkhttp 累计，用于结构化检索。成功最终打印 Info；HTTP 3xx/4xx 和主动取消打印 Warn；5xx、总超时、准备、钩子、传输、读取和解码错误打印 Error。

默认仅记录上述元数据，不记录 query 值、请求/响应正文、请求/响应 Header、Cookie、鉴权值和签名；首版不提供直接开启完整报文日志的开关。自定义日志字段显式传入，不自动反射请求对象。

每个请求的累加器与字段快照都独立，最终转成不会再修改的字段快照后输出。接口实现应同步保护自身字段 map，tkhttp 仍负责嵌套切片/map 的独立快照，不把 map 的锁当作嵌套对象的同步措施。通过接口 Skip 补偿固定打印层数，logx 的适配层也计算在内；Do 与便捷入口分别校准到业务调用处。

示例：一次请求成功，结束时打印一条类似日志：

```text
[INFO] {trace-123} caller/service.go:42 [18ms] HTTP 请求完成 {"biz":{"action":"queryDevice"},"http":{"method":"GET","url":"https://api.example.com/v1/devices","statusCode":200,"requestBytes":0,"responseBytes":128,"costMs":18,"phase":"complete","errorKind":""}}
```

取消后仍将合法的原 context 传给 LogAcc 接口，保留 trace 信息。打印方法不返回错误，输出失败的诊断由实现负责；采用 logx 时沿用其 stderr 诊断，不覆盖 HTTP 返回结果。进程崩溃、调用方或注入实现 panic、无效工厂结果和实际日志写入故障不属于“一次正常返回、一次输出调用”的保证。

## 使用示例

已有 Client 可直接省略请求配置，或按需覆盖本次超时：

```go
response, err := client.Get(ctx, "health")
// 处理 err；使用 Client 的默认配置。

response, err = client.Get(ctx, "health", tkhttp.RequestConfig{
	Timeout: 2 * time.Second,
})
// 处理 err；仅本次调用覆盖超时，其他参数仍使用 Client 默认值。
```

需要 query、JSON 解码和业务日志字段时传入一份 RequestConfig：

```go
func fetchDevices(ctx context.Context, factory tkhttp.LogAccFactory) (result map[string]any, err error) {
	client, err := tkhttp.New(
		tkhttp.Config{
			BaseURL: "https://api.example.com/v1/",
			Timeout: 5 * time.Second,
		},
		tkhttp.WithLogAcc(factory),
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

调用方传入一个 LogAccFactory，在函数内通过 `logx.NewLogAccAdapter(logx.WithLogger(backend))` 创建新实例，backend 为 adapter.Logger，可使用 GoFrame 适配器或 logx.NewLoggerAdapter(logger)。也可使用其他满足 adapter.LogAcc 契约的实现。实际服务在启动时创建并共享 Client，退出时清理自有空闲连接；每次调用传入 context 和目标地址，RequestConfig 按需省略或覆盖。只需要 HTTP 而不记录日志时，可使用 `Config{BaseURL: baseURL, DisableLog: true}` 省略日志工厂。

## 文件组织与验证建议

建议在 `toolkit/tkhttp/` 按 `client.go`、`config.go`、`options.go`、`request.go`、`response.go`、`hook.go`、`log.go`、`error.go` 和 `README.md` 组织。config.go 负责基本参数、默认值和校验，options.go 仅负责可选依赖注入。`adapter/log_acc.go` 声明 logx 与 tkhttp 实际共享的契约；`extension/logx/log_acc_adapter.go` 提供私有包装与适配构造函数，LogAcc 支持 SetStartTime，LogAccFactory 在 tkhttp 包内定义。仅供 tkhttp 使用的其他类型和辅助逻辑留在组件内部。

实现后应检查成功、准备失败、超时取消、HTTP 状态错误、读取超限和解码失败等路径；特别验证一次调用最多执行一次 http.Client.Do、结束只输出一条日志、并发请求字段隔离、签名字节与发送字节一致、敏感数据不进入日志，以及借用客户端和连接池的配置不被修改、借用池不被主动清理、注入的日志后端不被关闭。

连接池验证还应覆盖：未注入时创建独立默认池；单 Client 多次请求复用同一 Transport；多个 Client 注入同一池时保留实例身份和连接共享；WithTransport 与 WithHTTPClient 的组合顺序不改变优先级；注入客户端的 nil Transport 触发默认创建；显式 nil 和 typed nil 拒绝；组件仅清理自有池的空闲连接。实际连接复用通过本地 httptest 的连接状态或 httptrace 验证。

配置验证还应覆盖：填写 BaseURL 并注入非 nil 日志工厂时补齐合理默认值，关闭日志时可省略工厂；BaseURL 缺失直接报错；非法时长、限额和 Header 在 New 中拒绝；RequestConfig 零值、超时继承/禁用、typed nil Result、JSON null 和互斥请求体的行为一致；nil context 返回参数错误且能完成错误日志；所有参数错误都不发起网络请求。还应验证省略配置与零值配置等价、单份配置只覆盖当前请求、超时和 Header 正确继承、多个配置直接报错，以及连续和并发请求不污染 Client 默认值。

日志契约验证还应覆盖：开启日志但缺失工厂、显式 nil 工厂在 New 中拒绝；New 不调用工厂，每次请求只调用一次，返回 nil/typed nil 时在网络前报 ErrLogAcc；关闭日志不调用工厂、不修改起点；SetStartTime 在创建后能改变起点，零时间禁用耗时前缀，并发打印无数据竞争；工厂返回的新实例拥有独立的字段、起点和 Skip；不实现 Clone 的其他日志实现仍可使用；包装器的链式调用和源文件位置正确。tkhttp 的日志依赖只有 adapter，不导入 logx。

调用边界还应验证：tkhttp 不导入 tkretry，配置无重试参数；状态错误、网络错误或读取失败都直接返回，不重新调用底层客户端；日志字段无跨调用尝试统计。

用户已批准补充相关测试。验证采用 `httptest`、自定义 RoundTripper 和日志适配实现，覆盖取消、单次发送、URL 解析、资源关闭、日志数量和调用位置；仅对受影响的 `adapter`、`logx` 和 `tkhttp` 包运行 race 与 vet 检查，不连接真实平台或运行整个仓库集成测试。

## 参考

- [Go net/http：并发复用 Client/Transport、响应体关闭、context 和重定向](https://pkg.go.dev/net/http)
- [Go http.Transport：连接池参数、Clone 与空闲连接回收](https://pkg.go.dev/net/http#Transport)
- [Go net/url：ResolveReference 的相对地址规则](https://pkg.go.dev/net/url#URL.ResolveReference)
- [Go io：LimitReader 与有限读取](https://pkg.go.dev/io#LimitReader)
- [现有 LogAcc 使用约定](../../../extension/logx/README.md#logacc-累积日志字段)
