package tktoken

import (
	"errors"
	"fmt"
)

var (
	// ErrUnauthorized 表示令牌或会话未通过认证。
	ErrUnauthorized = errors.New("tktoken: unauthorized")
	// ErrNotFound 表示会话不存在。
	ErrNotFound = errors.New("tktoken: session not found")
	// ErrInvalidConfig 表示令牌配置或编解码器输入无效。
	ErrInvalidConfig = errors.New("tktoken: invalid configuration")
	// ErrInvalidSession 表示会话数据无效。
	ErrInvalidSession = errors.New("tktoken: invalid session")
	// ErrConflict 表示并发修改会话发生冲突。
	ErrConflict = errors.New("tktoken: concurrent session update")
)

func invalidConfig(reason string) (err error) {
	return fmt.Errorf("%w: %s", ErrInvalidConfig, reason)
}

func invalidCodecKey(cause error) (err error) {
	return fmt.Errorf("%w: AES key must contain 16, 24 or 32 bytes: %w", ErrInvalidConfig, cause)
}

func sessionNotFound() (err error) {
	return errors.Join(ErrUnauthorized, ErrNotFound)
}
