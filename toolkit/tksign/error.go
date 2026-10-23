package tksign

import (
	"errors"
	"fmt"
)

var (
	// ErrInvalidKey 表示密钥缺失、格式无效或密钥类型不受支持。
	ErrInvalidKey = errors.New("tksign: invalid key")
	// ErrMissingPrivateKey 表示当前实例不具备签名私钥。
	ErrMissingPrivateKey = errors.New("tksign: missing private key")
	// ErrMissingPublicKey 表示当前实例不具备验签公钥。
	ErrMissingPublicKey = errors.New("tksign: missing public key")
	// ErrUnsupportedOption 表示选项不适用于当前密钥。
	ErrUnsupportedOption = errors.New("tksign: unsupported option")
	// ErrVerification 表示签名编码无效或验签失败。
	ErrVerification = errors.New("tksign: signature verification failed")
	// ErrSigning 表示调用私钥签名失败，保留原始原因。
	ErrSigning = errors.New("tksign: signing failed")
)

func invalidKey(reason string) (err error) {
	return fmt.Errorf("%w: %s", ErrInvalidKey, reason)
}

func unsupportedOption(reason string) (err error) {
	return fmt.Errorf("%w: %s", ErrUnsupportedOption, reason)
}

func keyContextError(name string, cause error) (err error) {
	return fmt.Errorf("%s key: %w", name, cause)
}

func signingError(cause error) (err error) {
	return fmt.Errorf("%w: %w", ErrSigning, cause)
}

// verificationFailure 保持受控验签错误文本，原始原因仅通过错误链获取。
type verificationFailure struct{ cause error }

func (e *verificationFailure) Error() (message string) { return ErrVerification.Error() }
func (e *verificationFailure) Unwrap() (cause error)   { return e.cause }
func (e *verificationFailure) Is(target error) (matched bool) {
	return target == ErrVerification
}

func verificationError(cause error) (err error) {
	if errors.Is(cause, ErrVerification) {
		return cause
	}
	return &verificationFailure{cause: cause}
}
