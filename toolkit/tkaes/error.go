package tkaes

import (
	"errors"
	"fmt"
)

var (
	// ErrInvalidCiphertext 表示密文编码或分组长度无效。
	ErrInvalidCiphertext = errors.New("tkaes: invalid ciphertext")
	// ErrInvalidPadding 表示解密后的填充数据无效。
	ErrInvalidPadding = errors.New("tkaes: invalid padding")
)

func invalidCiphertext(message string, cause error) (err error) {
	if cause == nil {
		return fmt.Errorf("%w: %s", ErrInvalidCiphertext, message)
	}
	return fmt.Errorf("%w: %s: %w", ErrInvalidCiphertext, message, cause)
}

func invalidPadding(message string) (err error) {
	return fmt.Errorf("%w: %s", ErrInvalidPadding, message)
}
