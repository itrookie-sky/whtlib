// Package tksign 提供通过函数选项配置密钥的通用签名验签功能。
package tksign

import (
	"bytes"
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/base64"
	"encoding/hex"
)

// Signer 保存已解析的密钥和固定算法，初始化后可并发使用。
// 显式公钥用于验签，可以与签名私钥属于不同的密钥对或算法。
type Signer struct {
	privateKey crypto.Signer
	publicKey  crypto.PublicKey
	signHash   crypto.Hash
	verifyHash crypto.Hash
	rsaPSS     bool
	encoding   Encoding
}

// New 按需加载公私钥；只有私钥时自动导出验签公钥。
func New(opts ...Option) (signer *Signer, err error) {
	config := options{encoding: EncodingBase64}
	for _, option := range opts {
		if option != nil {
			option(&config)
		}
	}
	if !config.privateSet && !config.publicSet {
		err = invalidKey("at least one key is required")
		return
	}
	switch config.encoding {
	case EncodingBase64, EncodingBase64URL, EncodingHex:
	default:
		err = unsupportedOption("signature encoding")
		return
	}
	if config.hashSet && config.hash != crypto.SHA256 && config.hash != crypto.SHA384 && config.hash != crypto.SHA512 {
		err = unsupportedOption("hash must be SHA-256, SHA-384 or SHA-512")
		return
	}
	instance := &Signer{encoding: config.encoding, rsaPSS: config.rsaPSS}
	if config.privateSet {
		instance.privateKey, err = parsePrivateKey(config.privateKey, config.privateDER)
		if err != nil {
			err = keyContextError("private", err)
			return
		}
		instance.signHash, err = keyHash(instance.privateKey.Public(), config.hash, config.hashSet)
		if err != nil {
			return
		}
	}
	if config.publicSet {
		instance.publicKey, err = parsePublicKey(config.publicKey, config.publicDER)
		if err != nil {
			err = keyContextError("public", err)
			return
		}
	}
	if !config.publicSet && instance.privateKey != nil {
		instance.publicKey = instance.privateKey.Public()
	}
	instance.verifyHash, err = keyHash(instance.publicKey, config.hash, config.hashSet)
	if err != nil {
		return
	}
	if config.rsaPSS {
		_, publicRSA := instance.publicKey.(*rsa.PublicKey)
		privateRSA := false
		if instance.privateKey != nil {
			_, privateRSA = instance.privateKey.(*rsa.PrivateKey)
		}
		if !publicRSA && !privateRSA {
			err = unsupportedOption("RSA-PSS requires an RSA key")
			return
		}
	}
	return instance, nil
}

func keyHash(key crypto.PublicKey, requested crypto.Hash, explicit bool) (hash crypto.Hash, err error) {
	switch publicKey := key.(type) {
	case *rsa.PublicKey:
		if publicKey == nil || publicKey.N == nil || publicKey.N.Sign() <= 0 || publicKey.N.BitLen() < 1024 || publicKey.E < 2 || publicKey.E > 1<<31-1 {
			return 0, ErrInvalidKey
		}
		hash = crypto.SHA256
	case *ecdsa.PublicKey:
		switch publicKey.Curve {
		case elliptic.P256():
			hash = crypto.SHA256
		case elliptic.P384():
			hash = crypto.SHA384
		case elliptic.P521():
			hash = crypto.SHA512
		default:
			return 0, ErrInvalidKey
		}
	case ed25519.PublicKey:
		if len(publicKey) != ed25519.PublicKeySize {
			return 0, ErrInvalidKey
		}
		if explicit {
			return 0, unsupportedOption("Ed25519 signs the original message")
		}
		return 0, nil
	default:
		return 0, ErrInvalidKey
	}
	if explicit {
		hash = requested
	}
	return
}

// Sign 对原始消息签名，返回未经文本编码的签名字节。
func (s *Signer) Sign(data []byte) (signature []byte, err error) {
	if s == nil || s.privateKey == nil {
		return nil, ErrMissingPrivateKey
	}
	var opts crypto.SignerOpts = s.signHash
	if _, isRSA := s.privateKey.(*rsa.PrivateKey); isRSA && s.rsaPSS {
		opts = &rsa.PSSOptions{SaltLength: rsa.PSSSaltLengthEqualsHash, Hash: s.signHash}
	}
	signature, err = s.privateKey.Sign(rand.Reader, messageDigest(data, s.signHash), opts)
	if err != nil {
		err = signingError(err)
	}
	return
}

// Verify 使用配置的公钥验证原始消息和签名字节，失败时返回 ErrVerification。
func (s *Signer) Verify(data, signature []byte) (err error) {
	if s == nil || s.publicKey == nil {
		return ErrMissingPublicKey
	}
	digest := messageDigest(data, s.verifyHash)
	switch publicKey := s.publicKey.(type) {
	case *rsa.PublicKey:
		if s.rsaPSS {
			opts := &rsa.PSSOptions{SaltLength: rsa.PSSSaltLengthEqualsHash, Hash: s.verifyHash}
			err = rsa.VerifyPSS(publicKey, s.verifyHash, digest, signature, opts)
			break
		}
		err = rsa.VerifyPKCS1v15(publicKey, s.verifyHash, digest, signature)
	case *ecdsa.PublicKey:
		if !ecdsa.VerifyASN1(publicKey, digest, signature) {
			err = ErrVerification
		}
	case ed25519.PublicKey:
		if !ed25519.Verify(publicKey, data, signature) {
			err = ErrVerification
		}
	default:
		return ErrInvalidKey
	}
	if err != nil {
		err = verificationError(err)
	}
	return
}

func messageDigest(data []byte, hash crypto.Hash) (digest []byte) {
	switch hash {
	case crypto.SHA256:
		sum := sha256.Sum256(data)
		return sum[:]
	case crypto.SHA384:
		sum := sha512.Sum384(data)
		return sum[:]
	case crypto.SHA512:
		sum := sha512.Sum512(data)
		return sum[:]
	}
	return data
}

// SignString 对字符串原始字节签名，默认返回标准 Base64 签名。
func (s *Signer) SignString(data string) (signature string, err error) {
	raw, err := s.Sign([]byte(data))
	if err != nil {
		return
	}
	switch s.encoding {
	case EncodingBase64:
		signature = base64.StdEncoding.EncodeToString(raw)
	case EncodingBase64URL:
		signature = base64.RawURLEncoding.EncodeToString(raw)
	case EncodingHex:
		signature = hex.EncodeToString(raw)
	}
	return
}

// VerifyString 按固定编码解码签名，再验证字符串的原始字节。
func (s *Signer) VerifyString(data, signature string) (err error) {
	if s == nil || s.publicKey == nil {
		return ErrMissingPublicKey
	}
	var raw []byte
	switch s.encoding {
	case EncodingBase64:
		raw, err = base64.StdEncoding.Strict().DecodeString(signature)
	case EncodingBase64URL:
		raw, err = base64.RawURLEncoding.Strict().DecodeString(signature)
	case EncodingHex:
		raw, err = hex.DecodeString(signature)
	}
	if err != nil {
		return verificationError(err)
	}
	return s.Verify([]byte(data), raw)
}

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
