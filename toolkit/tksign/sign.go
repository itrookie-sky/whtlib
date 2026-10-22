package tksign

import (
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
	"fmt"
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
		err = fmt.Errorf("%w: at least one key is required", ErrInvalidKey)
		return
	}
	switch config.encoding {
	case EncodingBase64, EncodingBase64URL, EncodingHex:
	default:
		err = fmt.Errorf("%w: signature encoding", ErrUnsupportedOption)
		return
	}
	if config.hashSet && config.hash != crypto.SHA256 && config.hash != crypto.SHA384 && config.hash != crypto.SHA512 {
		err = fmt.Errorf("%w: hash must be SHA-256, SHA-384 or SHA-512", ErrUnsupportedOption)
		return
	}
	instance := &Signer{encoding: config.encoding, rsaPSS: config.rsaPSS}
	if config.privateSet {
		instance.privateKey, err = parsePrivateKey(config.privateKey, config.privateDER)
		if err != nil {
			err = fmt.Errorf("private key: %w", err)
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
			err = fmt.Errorf("public key: %w", err)
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
			err = fmt.Errorf("%w: RSA-PSS requires an RSA key", ErrUnsupportedOption)
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
			return 0, fmt.Errorf("%w: Ed25519 signs the original message", ErrUnsupportedOption)
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
		err = fmt.Errorf("tksign: signing failed: %w", err)
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
		err = ErrVerification
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
		return ErrVerification
	}
	return s.Verify([]byte(data), raw)
}
