package tkaes

import "errors"

var (
	// ErrInvalidCiphertext 表示密文编码或分组长度无效。
	ErrInvalidCiphertext = errors.New("tkaes: invalid ciphertext")
	// ErrInvalidPadding 表示解密后的填充数据无效。
	ErrInvalidPadding = errors.New("tkaes: invalid padding")
)

// dataError 保留原有报错文本，同时支持类别和底层解码错误匹配。
type dataError struct {
	kind    error
	message string
	cause   error
}

func (e *dataError) Error() (message string) { return e.message }
func (e *dataError) Unwrap() (cause error)   { return e.cause }
func (e *dataError) Is(target error) (matched bool) {
	return errors.Is(e.kind, target)
}

func invalidCiphertext(message string, cause error) (err error) {
	return &dataError{kind: ErrInvalidCiphertext, message: message, cause: cause}
}

func invalidPadding(message string) (err error) {
	return &dataError{kind: ErrInvalidPadding, message: message}
}
