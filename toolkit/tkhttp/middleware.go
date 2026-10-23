package tkhttp

import (
	"context"
	"sync"
)

// Handler 执行一次完整调用，返回响应快照和参数、读取、解码等最终错误。
// configs 保持公开请求入口的可选参数语义，校验在核心处理函数内执行。
type Handler func(ctx context.Context, method, target string, configs ...RequestConfig) (response *Response, err error)

// Middleware 包装完整调用；包装函数每次请求执行，不应在其中初始化共享资源。
// 返回的 Handler 应同步执行，每层 next 最多调用一次；共享状态由实现保证并发安全。
type Middleware func(next Handler) (handler Handler)

// middlewareCall 限制 next 的生命周期，避免重复发送或异步执行与最终日志竞争。
type middlewareCall struct {
	mu        sync.Mutex
	open      bool
	called    bool
	violation bool
	done      chan struct{}
}

func (call *middlewareCall) guard(next Handler) (handler Handler) {
	return func(ctx context.Context, method, target string, configs ...RequestConfig) (response *Response, err error) {
		call.mu.Lock()
		if !call.open || call.called {
			call.violation = true
			call.mu.Unlock()
			return nil, invalidMiddleware("next must be called synchronously and at most once")
		}
		call.called = true
		call.done = make(chan struct{})
		call.mu.Unlock()
		defer close(call.done)
		return next(ctx, method, target, configs...)
	}
}

func (call *middlewareCall) finish() (violation bool) {
	call.mu.Lock()
	call.open = false
	done := call.done
	if done != nil {
		select {
		case <-done:
		default:
			call.violation = true
		}
	}
	violation = call.violation
	call.mu.Unlock()
	// 不强行终止不配合的处理函数，等已开始的 next 退出后再访问执行状态。
	if done != nil {
		<-done
	}
	return
}

func wrapMiddleware(middleware Middleware, next Handler) (handler Handler) {
	return func(ctx context.Context, method, target string, configs ...RequestConfig) (response *Response, err error) {
		call := new(middlewareCall)
		defer func() {
			if call.finish() {
				err = middlewareViolation(err)
			}
		}()
		wrapped := middleware(call.guard(next))
		if wrapped == nil {
			return nil, invalidMiddleware("middleware returned nil Handler")
		}
		call.mu.Lock()
		invalid := call.violation
		call.open = !invalid
		call.mu.Unlock()
		if invalid {
			return nil, invalidMiddleware("middleware called next during construction")
		}
		response, err = wrapped(ctx, method, target, configs...)
		if response == nil && err == nil {
			return nil, invalidMiddleware("middleware returned nil response and nil error")
		}
		return response, middlewareError(err)
	}
}
