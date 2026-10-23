package tkhttp

import (
	"errors"
	"fmt"
)

var (
	// ErrInvalidConfig 表示客户端配置或注入依赖无效。
	ErrInvalidConfig = errors.New("tkhttp: invalid config")
	// ErrInvalidRequest 表示请求参数无效，未发起网络调用。
	ErrInvalidRequest = errors.New("tkhttp: invalid request")
	// ErrResponseTooLarge 表示响应超过配置的读取限额。
	ErrResponseTooLarge = errors.New("tkhttp: response too large")
	// ErrRequestHook 表示签名或鉴权钩子失败。
	ErrRequestHook = errors.New("tkhttp: request hook failed")
	// ErrLogAcc 表示请求日志模板不能克隆有效累加器。
	ErrLogAcc = errors.New("tkhttp: invalid log accumulator")
)

// RequestError 标识失败阶段；Error 不包含 URL、报文或底层原始错误文本。
// 通过 Unwrap、errors.Is 和 errors.AsType 检查原始原因。
type RequestError struct {
	Phase string // prepare、hook、transport、read 或 decode。
	Err   error  // 原始原因，仅显式解包时可见。
}

func (e *RequestError) Error() (message string) {
	return "tkhttp: " + e.Phase + " failed"
}

func (e *RequestError) Unwrap() (err error) {
	return e.Err
}

// StatusError 表示非 2xx 响应；响应报文由同时返回的 Response 保存。
type StatusError struct {
	StatusCode int // 本次响应状态码。
}

func (e *StatusError) Error() (message string) {
	return fmt.Sprintf("tkhttp: HTTP status %d", e.StatusCode)
}

func invalidRequest(reason string) (err error) {
	return fmt.Errorf("%w: %s", ErrInvalidRequest, reason)
}

func invalidConfig(reason string) (err error) {
	return fmt.Errorf("%w: %s", ErrInvalidConfig, reason)
}
