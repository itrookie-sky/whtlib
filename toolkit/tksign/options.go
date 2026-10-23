// Package tksign 提供通过函数选项配置密钥的通用签名验签功能。
package tksign

import (
	"bytes"
	"crypto"
)

// Encoding 表示字符串入口使用的签名编码。
type Encoding string

const (
	EncodingBase64    Encoding = "base64"
	EncodingBase64URL Encoding = "base64url"
	EncodingHex       Encoding = "hex"
)

// options 保存构造阶段的密钥内容与选项，供 New 统一校验和解析。
type options struct {
	privateKey []byte      // 私钥原始内容，按 privateDER 指定的方式解析。
	publicKey  []byte      // 公钥或证书原始内容，按 publicDER 指定的方式解析。
	privateSet bool        // 是否显式传入私钥，用于区分未配置与传入空密钥。
	publicSet  bool        // 是否显式传入公钥；未配置时允许从私钥导出，传入空值则校验失败。
	privateDER bool        // 私钥是否为二进制 DER；false 表示 PEM 或 Base64 DER 文本。
	publicDER  bool        // 公钥或证书是否为二进制 DER；false 表示 PEM 或 Base64 DER 文本。
	hash       crypto.Hash // 显式指定的 SHA-256、SHA-384 或 SHA-512；未指定时按密钥类型选择。
	hashSet    bool        // 是否显式指定摘要，用于拒绝无效摘要和 Ed25519 不兼容的摘要选项。
	rsaPSS     bool        // 是否启用 RSA-PSS，仅影响 RSA 密钥；默认使用 PKCS#1 v1.5。
	encoding   Encoding    // 字符串入口的签名编解码方式，默认 Base64，不影响原始字节入口。
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
