package tkredis

import (
	"errors"
	"fmt"
)

var (
	// ErrClosed 表示客户端已关闭。
	ErrClosed = errors.New("tkredis: client is closed")
	// ErrInvalidClient 表示底层客户端为空。
	ErrInvalidClient = errors.New("tkredis: native client is nil")
	// ErrInvalidCommand 表示命令为空。
	ErrInvalidCommand = errors.New("tkredis: command is empty")
	// ErrInvalidTTL 表示期限小于 -1，或毫秒取整后超出时长范围。
	ErrInvalidTTL = errors.New("tkredis: invalid ttl")
	// ErrScriptNotFound 表示脚本未注册。
	ErrScriptNotFound = errors.New("tkredis: script is not registered")
	// ErrLockHeld 表示当前锁实例已经持有租约。
	ErrLockHeld = errors.New("tkredis: this lock already holds a lease")
	// ErrLockLost 表示锁租约已经失去所有权。
	ErrLockLost = errors.New("tkredis: lock ownership was lost")
	// ErrInvalidArgument 表示参数校验或参数编码失败。
	ErrInvalidArgument = errors.New("tkredis: invalid argument")
	// ErrUnexpectedResponse 表示响应不符合组件约定的结构或类型。
	ErrUnexpectedResponse = errors.New("tkredis: unexpected response")
)

func invalidArgument(reason string) (err error) {
	return fmt.Errorf("%w: %s", ErrInvalidArgument, reason)
}

func unexpectedResponse(reason string) (err error) {
	return fmt.Errorf("%w: %s", ErrUnexpectedResponse, reason)
}

func argumentEncodingError(index int, cause error) (err error) {
	return fmt.Errorf("%w: JSON argument %d: %w", ErrInvalidArgument, index, cause)
}

func invalidCommand(index int) (err error) {
	return fmt.Errorf("%w at index %d", ErrInvalidCommand, index)
}

func scriptNotFound(name string) (err error) {
	return fmt.Errorf("%w: %s", ErrScriptNotFound, name)
}
