package tkua

import (
	"errors"
	"fmt"
)

var (
	// ErrInvalidConfig 表示配置不合法。
	ErrInvalidConfig = errors.New("tkua: invalid config")
	// ErrTooLong 表示输入超过解析器允许的字节数。
	ErrTooLong = errors.New("tkua: user agent too long")
	// ErrNotInitialized 表示解析器为 nil 或未经 New 初始化。
	ErrNotInitialized = errors.New("tkua: parser not initialized")
)

func invalidConfig() (err error) {
	return fmt.Errorf("%w: max length and cache size must not be negative", ErrInvalidConfig)
}

func tooLong(length, limit int) (err error) {
	return fmt.Errorf("%w: got %d bytes, limit %d", ErrTooLong, length, limit)
}

func initError(cause error) (err error) {
	return fmt.Errorf("tkua: initialize parser: %w", cause)
}
