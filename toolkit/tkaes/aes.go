package tkaes

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"encoding/base64"
	"encoding/hex"
)

const (
	EncodingTypebase16 = "base16"
	EncodingTypebase64 = "base64"
)

// AesUtils AES加密工具结构体
type AesUtils struct {
	key          []byte
	encodingType string // 编码类型，默认:base64,可选:base16
}

// NewAesUtils 使用自定义密钥创建 AES 工具实例。
// 密钥必须为 16、24 或 32 字节，分别对应 AES-128、AES-192 和 AES-256。
// 无效长度返回 nil 和标准库 aes.KeySizeError。
func NewAesUtils(key string, ops ...AesOptionFunc) (utils *AesUtils, err error) {
	switch len(key) {
	case 16, 24, 32:
	default:
		return nil, aes.KeySizeError(len(key))
	}
	utils = &AesUtils{
		key: []byte(key),
	}
	for _, op := range ops {
		op(utils)
	}
	return
}

type AesOptionFunc func(aes *AesUtils)

func WithEncodingType(encodingType string) AesOptionFunc {
	return func(aes *AesUtils) {
		aes.encodingType = encodingType
	}
}

// EncryptData 加密数据并返回Base64编码字符串
func (a *AesUtils) EncryptData(rawData string) (encryptData string, err error) {
	// 创建加密块
	block, err := aes.NewCipher(a.key)
	if err != nil {
		return "", err
	}

	// 填充原始数据
	paddedData := pkcs5Pad([]byte(rawData), block.BlockSize())

	// ECB模式不需要IV
	ciphertext := make([]byte, len(paddedData))
	for i := 0; i < len(paddedData); i += block.BlockSize() {
		block.Encrypt(ciphertext[i:], paddedData[i:i+block.BlockSize()])
	}
	switch a.encodingType {
	case EncodingTypebase16:
		return hex.EncodeToString(ciphertext), nil
	default:
		// 默认base64
		return base64.StdEncoding.EncodeToString(ciphertext), nil
	}
}

// DecryptData 解密Base64编码的加密数据
func (a *AesUtils) DecryptData(encryptData string) (rawData string, err error) {
	var ciphertext []byte
	// 解码类型
	switch a.encodingType {
	case EncodingTypebase16:
		ciphertext, err = hex.DecodeString(encryptData)
	default:
		ciphertext, err = base64.StdEncoding.DecodeString(encryptData)
	}
	if err != nil {
		return "", invalidCiphertext("invalid encrypted data", err)
	}

	// 创建解密块
	block, err := aes.NewCipher(a.key)
	if err != nil {
		return "", err
	}
	err = validateCiphertext(ciphertext, block.BlockSize())
	if err != nil {
		return "", err
	}

	// ECB模式解密
	plaintext := make([]byte, len(ciphertext))
	for bs, be := 0, block.BlockSize(); bs < len(ciphertext); bs += block.BlockSize() {
		block.Decrypt(plaintext[bs:be], ciphertext[bs:be])
		be += block.BlockSize()
	}

	// 去除填充
	unpadded, err := pkcs5Unpad(plaintext)
	if err != nil {
		return "", err
	}

	return string(unpadded), nil
}

// EncryptDataRawURL 加密数据并返回无填充的 URL 安全 Base64 字符串
func (a *AesUtils) EncryptDataRawURL(rawData string) (encryptData string, err error) {
	var block cipher.Block
	block, err = aes.NewCipher(a.key)
	if err != nil {
		return
	}

	paddedData := pkcs5Pad([]byte(rawData), block.BlockSize())
	ciphertext := make([]byte, len(paddedData))
	for i := 0; i < len(paddedData); i += block.BlockSize() {
		block.Encrypt(ciphertext[i:], paddedData[i:i+block.BlockSize()])
	}

	encryptData = base64.RawURLEncoding.EncodeToString(ciphertext)
	return
}

// DecryptDataRawURL 解码无填充的 URL 安全 Base64 字符串并解密数据
func (a *AesUtils) DecryptDataRawURL(encryptData string) (rawData string, err error) {
	var (
		block      cipher.Block
		ciphertext []byte
		plaintext  []byte
		unpadded   []byte
	)
	ciphertext, err = base64.RawURLEncoding.DecodeString(encryptData)
	if err != nil {
		err = invalidCiphertext("invalid encrypted data", err)
		return
	}

	block, err = aes.NewCipher(a.key)
	if err != nil {
		return
	}
	err = validateCiphertext(ciphertext, block.BlockSize())
	if err != nil {
		return
	}

	plaintext = make([]byte, len(ciphertext))
	for bs, be := 0, block.BlockSize(); bs < len(ciphertext); bs += block.BlockSize() {
		block.Decrypt(plaintext[bs:be], ciphertext[bs:be])
		be += block.BlockSize()
	}

	unpadded, err = pkcs5Unpad(plaintext)
	if err != nil {
		return
	}

	rawData = string(unpadded)
	return
}

// validateCiphertext 校验密文是否满足 AES 分组长度要求
func validateCiphertext(ciphertext []byte, blockSize int) (err error) {
	if len(ciphertext) == 0 || len(ciphertext)%blockSize != 0 {
		err = invalidCiphertext("invalid ciphertext length", nil)
		return
	}
	return
}

// pkcs5Pad PKCS5填充
func pkcs5Pad(data []byte, blockSize int) []byte {
	padding := blockSize - len(data)%blockSize
	padtext := bytes.Repeat([]byte{byte(padding)}, padding)
	return append(data, padtext...)
}

// pkcs5Unpad PKCS5去除填充
func pkcs5Unpad(data []byte) (unpadded []byte, err error) {
	length := len(data)
	if length == 0 {
		return nil, invalidPadding("invalid data length")
	}

	unpadding := int(data[length-1])
	if unpadding > length {
		return nil, invalidPadding("invalid padding size")
	}

	return data[:(length - unpadding)], nil
}
