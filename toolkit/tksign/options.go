// Package tksign 提供通过函数选项配置密钥的通用签名验签功能。
package tksign

import (
	"bytes"
	"crypto"
	"errors"
)

// Encoding 表示字符串入口使用的签名编码。
type Encoding string

const (
	EncodingBase64    Encoding = "base64"
	EncodingBase64URL Encoding = "base64url"
	EncodingHex       Encoding = "hex"
)

var (
	// ErrInvalidKey 表示密钥缺失、格式无效或密钥类型不受支持。
	ErrInvalidKey = errors.New("tksign: invalid key")
	// ErrMissingPrivateKey 表示当前实例不具备签名私钥。
	ErrMissingPrivateKey = errors.New("tksign: missing private key")
	// ErrMissingPublicKey 表示当前实例不具备验签公钥。
	ErrMissingPublicKey = errors.New("tksign: missing public key")
	// ErrUnsupportedOption 表示编码、摘要或算法选项不适用于当前密钥。
	ErrUnsupportedOption = errors.New("tksign: unsupported option")
	// ErrVerification 表示签名编码无效或签名与消息、公钥不匹配。
	ErrVerification = errors.New("tksign: signature verification failed")
)

type options struct {
	privateKey []byte
	publicKey  []byte
	privateSet bool
	publicSet  bool
	privateDER bool
	publicDER  bool
	hash       crypto.Hash
	hashSet    bool
	rsaPSS     bool
	encoding   Encoding
}

// Option 配置实例；同类选项以后一次为准。
type Option func(*options)

// WithPrivateKey 配置 PEM 或 Base64 DER 私钥，支持配置中的字面量换行符。
func WithPrivateKey(key string) (option Option) {
	return func(opts *options) {
		opts.privateKey = []byte(key)
		opts.privateSet = true
		opts.privateDER = false
	}
}

// WithPublicKey 配置 PEM 或 Base64 DER 公钥，也支持 X.509 证书。
func WithPublicKey(key string) (option Option) {
	return func(opts *options) {
		opts.publicKey = []byte(key)
		opts.publicSet = true
		opts.publicDER = false
	}
}

// WithPrivateKeyDER 配置二进制 DER 私钥，立即复制输入以支持选项复用。
func WithPrivateKeyDER(key []byte) (option Option) {
	key = bytes.Clone(key)
	return func(opts *options) {
		opts.privateKey = key
		opts.privateSet = true
		opts.privateDER = true
	}
}

// WithPublicKeyDER 配置二进制 DER 公钥或证书，立即复制输入。
func WithPublicKeyDER(key []byte) (option Option) {
	key = bytes.Clone(key)
	return func(opts *options) {
		opts.publicKey = key
		opts.publicSet = true
		opts.publicDER = true
	}
}

// WithHash 指定 SHA-256、SHA-384 或 SHA-512；不能用于 Ed25519。
func WithHash(hash crypto.Hash) (option Option) {
	return func(opts *options) {
		opts.hash = hash
		opts.hashSet = true
	}
}

// WithRSAPSS 将 RSA 签名和验签切换为 PSS，盐长度固定为摘要长度。
func WithRSAPSS() (option Option) {
	return func(opts *options) {
		opts.rsaPSS = true
	}
}

// WithEncoding 指定字符串入口的签名编码，默认使用标准 Base64。
func WithEncoding(encoding Encoding) (option Option) {
	return func(opts *options) {
		opts.encoding = encoding
	}
}
