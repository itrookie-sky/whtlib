package tkcache

import (
	"errors"
	"fmt"
)

var (
	// ErrClosed 表示缓存已关闭。
	ErrClosed = errors.New("tkcache: closed")
	// ErrInvalidArgument 表示缓存操作参数无效。
	ErrInvalidArgument = errors.New("tkcache: invalid argument")
	// ErrInvalidConfig 表示缓存配置或文件快照无效。
	ErrInvalidConfig = errors.New("tkcache: invalid config")
	// ErrLoaderPanic 表示回源函数发生 panic。
	ErrLoaderPanic = errors.New("tkcache: loader panic")
	// ErrDecode 表示命中缓存的 JSON 内容无法按目标类型解码。
	ErrDecode = errors.New("tkcache: JSON decode failed")
)

func invalidConfig(reason string) (err error) {
	return fmt.Errorf("%w: %s", ErrInvalidConfig, reason)
}

func invalidArgumentError(cause error) (err error) {
	return errors.Join(ErrInvalidArgument, cause)
}

func decodeError(cause error) (err error) {
	return errors.Join(ErrDecode, cause)
}

func multipleJSONValues() (err error) {
	return fmt.Errorf("%w: multiple JSON values", ErrDecode)
}

func loaderPanic(value any) (err error) {
	cause, ok := value.(error)
	if ok {
		return fmt.Errorf("%w: %w", ErrLoaderPanic, cause)
	}
	return fmt.Errorf("%w: %v", ErrLoaderPanic, value)
}
