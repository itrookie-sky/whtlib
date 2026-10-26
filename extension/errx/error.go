// Package errx 为标准错误链增加堆栈、结构化字段、错误码和诊断能力。
package errx

import (
	"maps"
	"slices"
)

// CodeError 携带业务错误码、说明和原始原因；具体错误码由使用方定义。
// 相同 Code 不自动视为 errors.Is 等价，类别判断继续使用原有哨兵错误。
type CodeError struct {
	Code    string // 稳定业务码；空值表示未指定。
	Message string // 面向调用方的说明，不自动附加原始错误。
	Err     error  // 原始原因，可通过 Unwrap 和 errors.Is/AsType 获取。
}

// Error 返回说明，缺失时依次回退到错误码、原始错误和默认说明。
func (e *CodeError) Error() (message string) {
	if e == nil {
		return
	}
	if e.Message != "" {
		return e.Message
	}
	if e.Code != "" {
		return e.Code
	}
	if e.Err != nil {
		return e.Err.Error()
	}
	return defaultErrorMessage
}

// Unwrap 保留原始错误链。
func (e *CodeError) Unwrap() (err error) {
	if e == nil {
		return
	}
	return e.Err
}

// ErrorCode 返回业务码，供 CodeOf 和外部适配使用。
func (e *CodeError) ErrorCode() (code string) {
	if e == nil {
		return
	}
	return e.Code
}

// stackError 增加调用堆栈，不改变原错误文本。
type stackError struct {
	err error
	pcs []uintptr
}

func (e *stackError) Error() (message string) { return e.err.Error() }
func (e *stackError) Unwrap() (err error)     { return e.err }

// StackTrace 返回独立的符号化堆栈。
func (e *stackError) StackTrace() (frames []Frame) { return stackFrames(e.pcs) }

// WithStack 在调用处记录最多 64 帧；树中已有非空堆栈时直接返回原错误。
// nil 返回 nil，不自动打印或将堆栈拼入 Error 文本。
func WithStack(err error) (wrapped error) {
	if err == nil {
		return
	}
	for current := range Walk(err) {
		if source, ok := current.(stackSource); ok && len(source.StackTrace()) > 0 {
			return err
		}
	}
	return &stackError{err: err, pcs: captureStack()}
}

// fieldsError 保存当前层的字段快照，不改变原错误文本。
type fieldsError struct {
	err    error
	fields map[string]any
}

func (e *fieldsError) Error() (message string) { return e.err.Error() }
func (e *fieldsError) Unwrap() (err error)     { return e.err }

// Fields 返回当前层字段 map 的浅复制。
func (e *fieldsError) Fields() (fields map[string]any) { return maps.Clone(e.fields) }

// WithFields 附加字段 map 的浅复制；nil 返回 nil，空字段返回原错误。
// 字段值中的引用类型仍由调用方管理和同步，不会自动出现在 Error 文本中。
func WithFields(err error, fields map[string]any) (wrapped error) {
	if err == nil {
		return
	}
	if len(fields) == 0 {
		return err
	}
	return &fieldsError{err: err, fields: maps.Clone(fields)}
}

// fieldsOf 提取当前节点的字段，不向下解包。
func fieldsOf(err error) (fields map[string]any) {
	if source, ok := err.(fieldsSource); ok {
		return maps.Clone(source.Fields())
	}
	return
}

// stackOf 提取当前节点的堆栈，不向下解包。
func stackOf(err error) (frames []Frame) {
	if source, ok := err.(stackSource); ok {
		return slices.Clone(source.StackTrace())
	}
	return
}

// codeOf 提取当前节点的业务码，不向下解包。
func codeOf(err error) (code string) {
	if source, ok := err.(codeSource); ok {
		return source.ErrorCode()
	}
	return
}
