package tkhttp

import (
	"net/http"

	"github.com/itrookie-sky/whtlib/adapter"
)

type options struct {
	transport    *http.Transport // 借用的显式标准库连接池。
	transportSet bool            // 区分未注入与显式 nil。
	httpClient   *http.Client    // 借用其公开配置，不复制内部状态。
	clientSet    bool            // 区分未注入与显式 nil。
	logAcc       adapter.LogAcc  // 只在构造时克隆为客户端内部模板。
	logAccSet    bool            // 显式 nil 即使关闭日志也视为配置错误。
	hook         RequestHook     // 可选签名或鉴权请求头钩子。
}

// Option 注入可选依赖，同类选项以后一次为准；nil 选项忽略。
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

// WithLogAcc 注入支持独立 Clone 的日志模板，不接管日志后端生命周期。
// 日志开启时必填；nil 和 typed nil 无效，关闭日志时不执行 Clone。
func WithLogAcc(logAcc adapter.LogAcc) (option Option) {
	return func(opts *options) {
		opts.logAcc = logAcc
		opts.logAccSet = true
	}
}

// WithRequestHook 注入发送前执行一次的请求头钩子；nil 表示不使用钩子。
func WithRequestHook(hook RequestHook) (option Option) {
	return func(opts *options) { opts.hook = hook }
}
