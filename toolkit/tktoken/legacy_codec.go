package tktoken

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"encoding/base64"
	"strings"
)

// LegacyCodec 使用旧 AES-CBC、PKCS7、标准 Base64 协议。
//
// 该协议使用固定 IV，未提供独立认证标签；仅供已有令牌协议迁移。
// 密钥必须由调用方显式提供，不包含默认密钥。
type LegacyCodec struct {
	block     cipher.Block
	delimiter string
}

// NewLegacyCodec 校验密钥并创建旧协议编解码器；空分隔符使用旧默认值。
func NewLegacyCodec(key []byte, delimiter string) (codec *LegacyCodec, err error) {
	if delimiter == "" {
		delimiter = DefaultTokenDelimiter
	}
	block, cipherErr := aes.NewCipher(bytes.Clone(key))
	if cipherErr != nil {
		err = invalidCodecKey(cipherErr)
		return
	}
	codec = &LegacyCodec{block: block, delimiter: delimiter}
	return
}

// Encode 将无歧义的用户标识和会话身份编码成旧格式令牌。
func (c *LegacyCodec) Encode(userKey, uuid string) (value string, err error) {
	if c == nil || c.block == nil || c.delimiter == "" {
		err = invalidConfig("legacy codec is not initialized")
		return
	}
	if userKey == "" || uuid == "" || strings.Contains(userKey, c.delimiter) || strings.Contains(uuid, c.delimiter) {
		err = invalidConfig("identity is empty or contains token delimiter")
		return
	}
	plain := []byte(userKey + c.delimiter + uuid)
	padding := aes.BlockSize - len(plain)%aes.BlockSize
	plain = append(plain, bytes.Repeat([]byte{byte(padding)}, padding)...)
	encrypted := make([]byte, len(plain))
	cipher.NewCBCEncrypter(c.block, []byte(legacyIV)).CryptBlocks(encrypted, plain)
	value = base64.StdEncoding.EncodeToString(encrypted)
	return
}

// Decode 严格检查 Base64、块长度和 PKCS7 填充，拒绝有歧义的身份。
func (c *LegacyCodec) Decode(value string) (identity Identity, err error) {
	if c == nil || c.block == nil || c.delimiter == "" {
		err = invalidConfig("legacy codec is not initialized")
		return
	}
	encrypted, decodeErr := base64.StdEncoding.Strict().DecodeString(value)
	if decodeErr != nil || len(encrypted) == 0 || len(encrypted)%aes.BlockSize != 0 {
		err = ErrUnauthorized
		return
	}
	plain := make([]byte, len(encrypted))
	cipher.NewCBCDecrypter(c.block, []byte(legacyIV)).CryptBlocks(plain, encrypted)
	padding := int(plain[len(plain)-1])
	if padding < 1 || padding > aes.BlockSize {
		err = ErrUnauthorized
		return
	}
	for _, value := range plain[len(plain)-padding:] {
		if value != byte(padding) {
			err = ErrUnauthorized
			return
		}
	}
	userKey, uuid, found := strings.Cut(string(plain[:len(plain)-padding]), c.delimiter)
	if !found || userKey == "" || uuid == "" || strings.Contains(uuid, c.delimiter) {
		err = ErrUnauthorized
		return
	}
	identity = Identity{UserKey: userKey, UUID: uuid}
	return
}
