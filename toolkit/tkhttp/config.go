// Package tkhttp 提供可注入标准库连接池和累计日志的单次 HTTP 调用。
package tkhttp

import (
	"math"
	"net/http"
	"net/url"
	"slices"
	"time"

	"github.com/itrookie-sky/whtlib/adapter"
)

// Config 配置客户端基本参数；依赖通过 With 选项注入。
type Config struct {
	BaseURL          string        // 必填 HTTP(S) 绝对地址，不允许 userinfo、query 或 fragment。
	DefaultHeader    http.Header   // 默认请求头，New 复制后固定。
	Timeout          time.Duration // 0 默认 10s，精确 time.Duration(-1) 不添加组件超时。
	MaxResponseBytes int64         // 0 默认 8 MiB，正数限制实际读取的响应字节。
	DisableLog       bool          // true 关闭汇总日志；默认必须注入有效 LogAcc。
}

// RequestConfig 按需覆盖当前请求；省略或零值均沿用客户端默认值。
type RequestConfig struct {
	Header    http.Header    // 按字段覆盖默认请求头，不修改传入容器。
	Query     url.Values     // 合并 URL query，同名键整体替换并保留多值。
	JSON      any            // nil 表示未设置；其他输入按 JSON 编码一次。
	Form      url.Values     // nil 表示未设置；空集合仍为显式表单体。
	Body      []byte         // nil 表示未设置；空切片仍为显式原始请求体。
	Result    any            // 可选 JSON 解码目标，必须为非 nil 指针。
	Timeout   time.Duration  // 0 继承 Client，正数覆盖，精确 -1 禁用组件超时。
	LogFields map[string]any // 显式非敏感业务字段，放入日志 biz 组。
}

func normalizeConfig(config Config) (normalized Config, baseURL *url.URL, err error) {
	normalized = config
	baseURL, err = parseBaseURL(config.BaseURL)
	if err != nil {
		return
	}
	if config.Timeout < -1 {
		err = invalidConfig("Timeout must be positive, zero or -1")
		return
	}
	if normalized.Timeout == 0 {
		normalized.Timeout = 10 * time.Second
	}
	if config.MaxResponseBytes < 0 || config.MaxResponseBytes == math.MaxInt64 {
		err = invalidConfig("MaxResponseBytes must be positive and allow one extra byte")
		return
	}
	if normalized.MaxResponseBytes == 0 {
		normalized.MaxResponseBytes = 8 << 20
	}
	normalized.DefaultHeader, err = copyHeader(config.DefaultHeader)
	if err != nil {
		err = invalidConfig("DefaultHeader contains invalid or transport-controlled fields")
		return
	}
	return
}

// LogAccFactory 为每次调用创建独立的日志累加器。
// 工厂应支持并发调用，不复用累加器；日志后端可共享，由调用方管理。
type LogAccFactory func() (acc adapter.LogAcc)

type options struct {
	transport     *http.Transport // 借用的显式标准库连接池。
	transportSet  bool            // 区分未注入与显式 nil。
	httpClient    *http.Client    // 借用其公开配置，不复制内部状态。
	clientSet     bool            // 区分未注入与显式 nil。
	logAccFactory LogAccFactory   // 每次调用创建独立累加器，不共享字段、起点或 Skip。
	logAccSet     bool            // 显式 nil 即使关闭日志也视为配置错误。
	hook          RequestHook     // 可选签名或鉴权请求头钩子。
	middlewares   []Middleware    // 按注册顺序追加的完整调用层中间件。
}

// Option 注入可选依赖；单值选项以后一次为准，中间件追加，nil 选项忽略。
type Option func(*options)

// WithTransport 借用标准库连接池，优先于 WithHTTPClient 中的 Transport。
// 不克隆、不修改、不清理注入池，生命周期由调用方管理；显式 nil 无效。
func WithTransport(transport *http.Transport) (option Option) {
	return func(opts *options) {
		opts.transport = transport
		opts.transportSet = true
	}
}

// WithHTTPClient 复制客户端公开配置，借用 Transport 和 Jar；显式 nil 无效。
// Transport 为 nil 且未指定 WithTransport 时创建默认池；CheckRedirect 被禁止重定向规则覆盖。
func WithHTTPClient(client *http.Client) (option Option) {
	return func(opts *options) {
		opts.httpClient = client
		opts.clientSet = true
	}
}

// WithLogAcc 注入日志创建函数，每次调用返回一个独立累加器。
// 日志开启时必填，显式 nil 无效；New 和关闭日志时不调用创建函数。
// 工厂应支持并发调用；日志后端由调用方管理。
func WithLogAcc(factory LogAccFactory) (option Option) {
	return func(opts *options) {
		opts.logAccFactory = factory
		opts.logAccSet = true
	}
}

// WithRequestHook 注入发送前执行一次的请求头钩子；nil 表示不使用钩子。
func WithRequestHook(hook RequestHook) (option Option) {
	return func(opts *options) { opts.hook = hook }
}

// WithMiddleware 按顺序追加完整调用层中间件，第一个位于最外层。
// 列表在创建选项时复制；空列表保持原行为，nil 中间件使 New 返回 ErrInvalidConfig。
func WithMiddleware(middlewares ...Middleware) (option Option) {
	selected := slices.Clone(middlewares)
	return func(opts *options) { opts.middlewares = append(opts.middlewares, selected...) }
}
