package tktoken

import (
	"crypto/aes"
	"crypto/cipher"
	"encoding/base64"
	"errors"
	"strings"
	"testing"
)

func TestLegacyCodecGolden(t *testing.T) {
	key := []byte("0123456789abcdef")
	codec, err := NewLegacyCodec(key, "_")
	if err != nil {
		t.Fatal(err)
	}
	// 测试向量由 OpenSSL AES-128-CBC、PKCS7 和 gaes 默认 IV 独立生成。
	const golden = "1TKDvDtZaHDNN1w7nApAtgaLa+bPmjg/eN53n21O5Oy7wyZ/AlLkmjD2eJdhnwRe"
	const uuid = "0123456789abcdef0123456789abcdef"
	key[0] = 'x'
	value, err := codec.Encode("user", uuid)
	if err != nil {
		t.Fatal(err)
	}
	if value != golden {
		t.Fatal("旧 CBC 协议测试向量不匹配")
	}
	identity, err := codec.Decode(golden)
	if err != nil || identity.UserKey != "user" || identity.UUID != uuid {
		t.Fatal("旧令牌解码结果不匹配")
	}
	_, err = codec.Encode("user_name", uuid)
	if !errors.Is(err, ErrInvalidConfig) {
		t.Fatal("有歧义的用户标识应被拒绝")
	}
}

func TestLegacyCodecRejectsMalformedInput(t *testing.T) {
	codec, err := NewLegacyCodec([]byte("0123456789abcdef"), "_")
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{"", "%%%", base64.StdEncoding.EncodeToString([]byte("short")), strings.Repeat("A", 24)} {
		_, err = codec.Decode(value)
		if !errors.Is(err, ErrUnauthorized) {
			t.Fatalf("非法编码应返回 unauthorized，实际 %v", err)
		}
	}
	block, err := aes.NewCipher([]byte("0123456789abcdef"))
	if err != nil {
		t.Fatal(err)
	}
	for _, plain := range [][]byte{
		[]byte("user_uuidxxxxxx\x00"),
		[]byte("user_uuidxxxxx\x01\x02"),
		[]byte("user_uuidxxxxxx\x11"),
	} {
		encoded := make([]byte, len(plain))
		cipher.NewCBCEncrypter(block, []byte(legacyIV)).CryptBlocks(encoded, plain)
		_, err = codec.Decode(base64.StdEncoding.EncodeToString(encoded))
		if !errors.Is(err, ErrUnauthorized) {
			t.Fatal("非法 PKCS7 填充应被拒绝")
		}
	}
	for _, key := range [][]byte{nil, []byte("short"), make([]byte, 17)} {
		_, err = NewLegacyCodec(key, "_")
		if !errors.Is(err, ErrInvalidConfig) {
			t.Fatal("非法密钥长度应被拒绝")
		}
	}
}
