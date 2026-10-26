package tkhttp

import (
	"context"
	"errors"
	"fmt"
)

var (
	// ErrInvalidConfig 表示客户端配置或注入依赖无效。
	ErrInvalidConfig = errors.New("tkhttp: invalid config")
	// ErrInvalidRequest 表示请求参数无效，未发起网络调用。
	ErrInvalidRequest = errors.New("tkhttp: invalid request")
	// ErrRequestEncode 表示请求正文编码失败。
	ErrRequestEncode = errors.New("tkhttp: request encode failed")
	// ErrTransport 表示 HTTP 发送阶段失败。
	ErrTransport = errors.New("tkhttp: transport failed")
	// ErrResponseRead 表示响应读取失败，包括超过读取限额。
	ErrResponseRead = errors.New("tkhttp: response read failed")
	// ErrResponseTooLarge 表示响应超过配置的读取限额。
	ErrResponseTooLarge = errors.New("tkhttp: response too large")
	// ErrRequestHook 表示签名或鉴权钩子失败。
	ErrRequestHook = errors.New("tkhttp: request hook failed")
	// ErrLogAcc 表示日志创建函数返回 nil 或 typed nil 累加器。
	ErrLogAcc = errors.New("tkhttp: invalid log accumulator")
	// ErrHTTPStatus 表示 HTTP 状态码非 2xx，状态码保存在 StatusError 中。
	ErrHTTPStatus = errors.New("tkhttp: HTTP status failed")
	// ErrResponseDecode 表示 JSON 响应解码失败。
	ErrResponseDecode = errors.New("tkhttp: response decode failed")
	// ErrMiddleware 表示中间件失败、结果无效或违反 next 调用约定。
	ErrMiddleware = errors.New("tkhttp: middleware failed")
)

// RequestError 标识失败阶段；Error 不包含 URL、报文或底层原始错误文本。
// 通过 Unwrap、errors.Is 和 errors.AsType 检查原始原因。
type RequestError struct {
	Phase string // 失败阶段，使用 PhasePrepare、PhaseHook 等阶段常量判断。
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

// Is 支持按包级类别匹配，具体状态码仍通过 errors.AsType 获取。
func (e *StatusError) Is(target error) (matched bool) {
	return target == ErrHTTPStatus
}

// withErrorKind 增加稳定类别，保留原始原因且避免重复包装。
func withErrorKind(kind, cause error) (err error) {
	if cause == nil {
		return kind
	}
	if errors.Is(cause, kind) {
		return cause
	}
	return errors.Join(kind, cause)
}

func (s *requestLog) failure(cause error) (err error) {
	switch s.phase {
	case PhaseHook:
		cause = withErrorKind(ErrRequestHook, cause)
	case PhaseTransport:
		cause = withErrorKind(ErrTransport, cause)
	case PhaseRead:
		cause = withErrorKind(ErrResponseRead, cause)
	case PhaseDecode:
		cause = withErrorKind(ErrResponseDecode, cause)
	case PhaseMiddleware:
		cause = withErrorKind(ErrMiddleware, cause)
	}
	return &RequestError{Phase: s.phase, Err: cause}
}

func invalidMiddleware(reason string) (err error) {
	return &RequestError{Phase: PhaseMiddleware, Err: fmt.Errorf("%w: %s", ErrMiddleware, reason)}
}

func middlewareViolation(cause error) (err error) {
	return &RequestError{Phase: PhaseMiddleware, Err: errors.Join(ErrMiddleware, cause)}
}

func middlewareError(cause error) (err error) {
	if cause == nil {
		return
	}
	_, requestError := errors.AsType[*RequestError](cause)
	_, statusError := errors.AsType[*StatusError](cause)
	if requestError || statusError {
		return cause
	}
	return &RequestError{Phase: PhaseMiddleware, Err: withErrorKind(ErrMiddleware, cause)}
}

func contextResult(ctx context.Context, original error) (err error) {
	err = ctx.Err()
	if err == nil {
		return original
	}
	return errors.Join(err, original)
}

func invalidRequest(reason string) (err error) {
	return fmt.Errorf("%w: %s", ErrInvalidRequest, reason)
}

func invalidConfig(reason string) (err error) {
	return fmt.Errorf("%w: %s", ErrInvalidConfig, reason)
}
