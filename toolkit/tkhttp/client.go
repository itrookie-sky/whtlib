package tkhttp

import (
	"context"
	"net"
	"net/http"
	"net/url"
	"time"

	"github.com/itrookie-sky/whtlib/adapter"
)

// Client 固定配置并复用连接池，支持并发调用；应通过 New 创建。
// 不提供重试能力，每次调用最多执行一次 http.Client.Do。
type Client struct {
	config     Config          // 归一化后的私有默认配置。
	baseURL    *url.URL        // 已校验的基础地址。
	httpClient *http.Client    // 仅复制公开配置的私有客户端。
	ownedPool  *http.Transport // 非 nil 表示连接池由当前 Client 创建。
	logAcc     adapter.LogAcc  // 内部固定模板，每次调用克隆。
	hook       RequestHook     // 初始化后固定的请求头钩子。
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
	if selected.logAccSet && isNil(selected.logAcc) || !config.DisableLog && isNil(selected.logAcc) {
		err = invalidConfig("WithLogAcc requires a non-nil accumulator when logging is enabled")
		return
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
	if native.Transport != nil && isNil(native.Transport) {
		err = invalidConfig("selected Transport must not be typed nil")
		return
	}
	var template adapter.LogAcc
	if !config.DisableLog {
		template = selected.logAcc.Clone()
		if isNil(template) {
			err = invalidConfig("LogAcc.Clone returned an invalid accumulator")
			return
		}
	}
	var owned *http.Transport
	if native.Transport == nil {
		owned = newDefaultPool()
		native.Transport = owned
	}
	client = &Client{
		config:     config,
		baseURL:    baseURL,
		httpClient: native,
		ownedPool:  owned,
		logAcc:     template,
		hook:       selected.hook,
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
	state := requestLog{start: time.Now(), phase: "prepare"}
	if c == nil || c.httpClient == nil {
		err = state.failure(invalidConfig("Client must be created with New"))
		return
	}
	if !c.config.DisableLog {
		state.acc = c.logAcc.Clone()
		if isNil(state.acc) {
			err = state.failure(ErrLogAcc)
			return
		}
		state.acc.SetStartTime(state.start)
		// finish、do 和公开请求入口各占一层；适配层由实现自行补偿。
		state.acc.Skip(3)
	}
	logContext := ctx
	if isNil(logContext) {
		logContext = context.Background()
	}
	defer state.finish(logContext, &response, &err)
	if validToken(method) {
		state.method = method
	}
	if isNil(ctx) {
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
		state.phase = "hook"
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
	state.phase = "transport"
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
	state.phase = "read"
	response, state.responseBytes, err = readResponse(native, c.config.MaxResponseBytes)
	err = contextResult(ctx, err)
	if err != nil {
		err = state.failure(err)
		return
	}
	state.phase = "status"
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		err = &StatusError{StatusCode: response.StatusCode}
		return
	}
	state.phase = "decode"
	err = decodeResponse(response, method, config.Result)
	err = contextResult(ctx, err)
	if err != nil {
		err = state.failure(err)
		return
	}
	state.phase = "complete"
	return
}

func contextResult(ctx context.Context, original error) (err error) {
	err = ctx.Err()
	if err != nil {
		return
	}
	return original
}
