package tksign

import (
	"bytes"
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/rsa"
	"crypto/x509"
	"encoding/asn1"
	"encoding/base64"
	"encoding/pem"
	"strings"
)

// decodeKey 解开文本封装；DER 输入保持原始字节，避免裁掉合法二进制数据。
func decodeKey(input []byte, rawDER, private bool) (der []byte, err error) {
	if len(input) == 0 {
		err = ErrInvalidKey
		return
	}
	if rawDER {
		return input, nil
	}
	text := strings.TrimSpace(string(input))
	text = strings.NewReplacer(`\r\n`, "\n", `\n`, "\n", `\r`, "\r").Replace(text)
	if strings.HasPrefix(text, "-----BEGIN ") {
		if strings.Count(text, "-----BEGIN ") != 1 {
			return nil, ErrInvalidKey
		}
		block, rest := pem.Decode([]byte(text))
		if block == nil || len(bytes.TrimSpace(rest)) != 0 || len(block.Headers) != 0 {
			err = ErrInvalidKey
			return
		}
		if !validPEMType(block.Type, private) {
			err = ErrInvalidKey
			return
		}
		return block.Bytes, nil
	}
	text = strings.Join(strings.Fields(text), "")
	for _, encoding := range []*base64.Encoding{base64.StdEncoding.Strict(), base64.RawStdEncoding.Strict()} {
		der, err = encoding.DecodeString(text)
		if err == nil && len(der) > 0 {
			return
		}
	}
	return nil, ErrInvalidKey
}

func validPEMType(blockType string, private bool) (valid bool) {
	if private {
		switch blockType {
		case "PRIVATE KEY", "RSA PRIVATE KEY", "EC PRIVATE KEY":
			return true
		}
		return false
	}
	switch blockType {
	case "PUBLIC KEY", "RSA PUBLIC KEY", "CERTIFICATE":
		return true
	}
	return false
}

func parsePrivateKey(input []byte, rawDER bool) (key crypto.Signer, err error) {
	der, err := decodeKey(input, rawDER, true)
	if err != nil {
		return
	}
	if !validKeyDER(der) {
		return nil, ErrInvalidKey
	}
	parsed, parseErr := x509.ParsePKCS8PrivateKey(der)
	if parseErr == nil {
		switch privateKey := parsed.(type) {
		case *rsa.PrivateKey:
			return privateKey, nil
		case *ecdsa.PrivateKey:
			return privateKey, nil
		case ed25519.PrivateKey:
			return privateKey, nil
		}
		return nil, ErrInvalidKey
	}
	key, err = x509.ParsePKCS1PrivateKey(der)
	if err == nil {
		return
	}
	key, err = x509.ParseECPrivateKey(der)
	if err == nil {
		return
	}
	return nil, ErrInvalidKey
}

func parsePublicKey(input []byte, rawDER bool) (key crypto.PublicKey, err error) {
	der, err := decodeKey(input, rawDER, false)
	if err != nil {
		return
	}
	if !validKeyDER(der) {
		return nil, ErrInvalidKey
	}
	key, err = x509.ParsePKIXPublicKey(der)
	if err == nil {
		return
	}
	key, err = x509.ParsePKCS1PublicKey(der)
	if err == nil {
		return
	}
	certificate, parseErr := x509.ParseCertificate(der)
	if parseErr == nil {
		return certificate.PublicKey, nil
	}
	return nil, ErrInvalidKey
}

// validKeyDER 校验单个完整序列，补足部分标准库解析器对尾随数据的宽松处理。
func validKeyDER(der []byte) (valid bool) {
	var value asn1.RawValue
	rest, err := asn1.Unmarshal(der, &value)
	if err != nil {
		return false
	}
	return len(rest) == 0 && value.Class == asn1.ClassUniversal && value.Tag == asn1.TagSequence && value.IsCompound
}
