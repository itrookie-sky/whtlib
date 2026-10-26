package tkhttp

import (
	"context"
	"net"
	"net/http"
	"net/url"
	"slices"
	"time"

	"github.com/itrookie-sky/whtlib/extension/reflectx"
)

// Client 固定配置并复用连接池，支持并发调用；应通过 New 创建。
// 不提供重试能力，每次调用最多执行一次 http.Client.Do。
type Client struct {
	config        Config          // 归一化后的私有默认配置。
	baseURL       *url.URL        // 已校验的基础地址。
	httpClient    *http.Client    // 仅复制公开配置的私有客户端。
	ownedPool     *http.Transport // 非 nil 表示连接池由当前 Client 创建。
	logAccFactory LogAccFactory   // 每次调用创建独立累加器。
	hook          RequestHook     // 初始化后固定的请求头钩子。
	middlewares   []Middleware    // 初始化后固定的完整调用层中间件。
}

// New 复制配置、补齐默认值并校验依赖；不主动建立网络连接。
func New(config Config, opts ...Option) (client *Client, err error) {
	config, baseURL, err := normalizeConfig(config)
	if err != nil {
		return
	}
	var selected options
	for _, opt := range opts {
		if opt != nil {
			opt(&selected)
		}
	}
	if selected.transportSet && selected.transport == nil {
		err = invalidConfig("WithTransport requires a non-nil transport")
		return
	}
	if selected.clientSet && selected.httpClient == nil {
		err = invalidConfig("WithHTTPClient requires a non-nil client")
		return
	}
	if (selected.logAccSet || !config.DisableLog) && selected.logAccFactory == nil {
		err = invalidConfig("WithLogAcc requires a non-nil factory when logging is enabled")
		return
	}
	for _, middleware := range selected.middlewares {
		if middleware == nil {
			err = invalidConfig("WithMiddleware requires non-nil middleware")
			return
		}
	}
	native := &http.Client{
		CheckRedirect: func(request *http.Request, via []*http.Request) (err error) {
			return http.ErrUseLastResponse
		},
	}
	if selected.httpClient != nil {
		native.Transport = selected.httpClient.Transport
		native.Jar = selected.httpClient.Jar
		native.Timeout = selected.httpClient.Timeout
	}
	if selected.transportSet {
		native.Transport = selected.transport
	}
	if native.Transport != nil && reflectx.IsNil(native.Transport) {
		err = invalidConfig("selected Transport must not be typed nil")
		return
	}
	var owned *http.Transport
	if native.Transport == nil {
		owned = newDefaultPool()
		native.Transport = owned
	}
	client = &Client{
		config:        config,
		baseURL:       baseURL,
		httpClient:    native,
		ownedPool:     owned,
		logAccFactory: selected.logAccFactory,
		hook:          selected.hook,
		middlewares:   slices.Clone(selected.middlewares),
	}
	return
}

func newDefaultPool() (transport *http.Transport) {
	return &http.Transport{
		Proxy: http.ProxyFromEnvironment,
		DialContext: (&net.Dialer{
			Timeout:   30 * time.Second,
			KeepAlive: 30 * time.Second,
		}).DialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          100,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: time.Second,
	}
}

// CloseIdleConnections 仅清理自建池的空闲连接；借用池时为空操作。
// 不取消正在执行的请求，不关闭借用的客户端、池或日志后端。
func (c *Client) CloseIdleConnections() {
	if c != nil && c.ownedPool != nil {
		c.ownedPool.CloseIdleConnections()
	}
}

// Do 执行单次 HTTP 调用并完整读取限额内响应；最多允许一份 RequestConfig。
// 返回响应无需调用方关闭；非 2xx 返回 StatusError，同时保留 Response。
func (c *Client) Do(ctx context.Context, method, target string, configs ...RequestConfig) (response *Response, err error) {
	return c.do(ctx, method, target, configs)
}

// Get 使用 GET 方法调用，省略配置时使用 Client 默认值。
func (c *Client) Get(ctx context.Context, target string, configs ...RequestConfig) (response *Response, err error) {
	return c.do(ctx, http.MethodGet, target, configs)
}

// Post 使用 POST 方法调用，正文通过可选 RequestConfig 设置。
func (c *Client) Post(ctx context.Context, target string, configs ...RequestConfig) (response *Response, err error) {
	return c.do(ctx, http.MethodPost, target, configs)
}

func (c *Client) do(ctx context.Context, method, target string, configs []RequestConfig) (response *Response, err error) {
	state := requestLog{start: time.Now(), phase: PhasePrepare}
	if c == nil || c.httpClient == nil {
		err = state.failure(invalidConfig("Client must be created with New"))
		return
	}
	if !c.config.DisableLog {
		state.acc = c.logAccFactory()
		if reflectx.IsNil(state.acc) {
			err = state.failure(ErrLogAcc)
			return
		}
		state.acc.SetStartTime(state.start)
		// finish、do 和公开请求入口各占一层；适配层由实现自行补偿。
		state.acc.Skip(3)
	}
	logContext := ctx
	if reflectx.IsNil(logContext) {
		logContext = context.Background()
	}
	defer state.finish(logContext, &response, &err)
	handler := Handler(func(ctx context.Context, method, target string, configs ...RequestConfig) (response *Response, err error) {
		return c.execute(ctx, method, target, configs, &state)
	})
	for _, middleware := range slices.Backward(c.middlewares) {
		handler = wrapMiddleware(middleware, handler)
	}
	if validToken(method) {
		state.method = method
	}
	response, err = handler(ctx, method, target, configs...)
	state.result(err)
	return
}

func (c *Client) execute(ctx context.Context, method, target string, configs []RequestConfig, state *requestLog) (response *Response, err error) {
	if validToken(method) {
		state.method = method
	}
	if reflectx.IsNil(ctx) {
		err = state.failure(invalidRequest("context must not be nil"))
		return
	}
	config, err := selectConfig(configs)
	if err != nil {
		err = state.failure(err)
		return
	}
	if state.acc != nil {
		state.biz = cloneLogFields(config.LogFields)
	}
	timeout := config.Timeout
	if timeout == 0 {
		timeout = c.config.Timeout
	}
	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithDeadline(ctx, state.start.Add(timeout))
		defer cancel()
	}
	err = ctx.Err()
	if err != nil {
		err = state.failure(err)
		return
	}
	request, body, err := c.prepare(ctx, method, target, config)
	if err != nil {
		err = state.failure(contextResult(ctx, err))
		return
	}
	state.url = (&url.URL{Scheme: request.URL.Scheme, Host: request.URL.Host, Path: request.URL.Path, RawPath: request.URL.RawPath}).String()
	state.requestBytes = len(body)
	err = ctx.Err()
	if err != nil {
		err = state.failure(err)
		return
	}
	if c.hook != nil {
		state.phase = PhaseHook
		err = c.applyHook(ctx, request, body)
		if err != nil {
			err = state.failure(contextResult(ctx, err))
			return
		}
	}
	err = ctx.Err()
	if err != nil {
		err = state.failure(err)
		return
	}
	state.phase = PhaseTransport
	native, err := c.httpClient.Do(request)
	if err != nil {
		if native != nil {
			response = responseMetadata(native)
			if native.Body != nil {
				_ = native.Body.Close()
			}
		}
		err = state.failure(contextResult(ctx, err))
		return
	}
	state.phase = PhaseRead
	response, state.responseBytes, err = readResponse(native, c.config.MaxResponseBytes)
	err = contextResult(ctx, err)
	if err != nil {
		err = state.failure(err)
		return
	}
	state.phase = PhaseStatus
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		err = &StatusError{StatusCode: response.StatusCode}
		return
	}
	state.phase = PhaseDecode
	err = decodeResponse(response, method, config.Result)
	err = contextResult(ctx, err)
	if err != nil {
		err = state.failure(err)
		return
	}
	state.phase = PhaseComplete
	return
}
