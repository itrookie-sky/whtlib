package aes

import (
	"crypto/aes"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"testing"
)

func TestNewAesUtilsKeyLength(t *testing.T) {
	for _, size := range []int{0, 15, 16, 17, 23, 24, 25, 31, 32, 33} {
		t.Run(fmt.Sprintf("bytes_%d", size), func(t *testing.T) {
			utils, err := NewAesUtils(strings.Repeat("k", size))
			valid := size == 16 || size == 24 || size == 32
			if valid {
				if err != nil || utils == nil {
					t.Fatalf("valid key rejected: %v", err)
				}
			} else {
				keyErr, ok := errors.AsType[aes.KeySizeError](err)
				if utils != nil || !ok || int(keyErr) != size {
					t.Fatalf("want nil and KeySizeError(%d), got %v, %v", size, utils, err)
				}
			}
			t.Logf("密钥字节数=%d，实例创建成功=%t，错误=%v", size, utils != nil, err)
		})
	}
	// 中文字符按 UTF-8 字节数校验，而不是按字符数校验。
	for _, key := range []string{strings.Repeat("中", 8), strings.Repeat("中", 16)} {
		utils, err := NewAesUtils(key)
		if (len(key) == 24) != (err == nil && utils != nil) {
			t.Fatalf("unexpected UTF-8 key result: bytes=%d err=%v", len(key), err)
		}
		t.Logf("中文密钥字符数=%d，字节数=%d，错误=%v", len([]rune(key)), len(key), err)
	}
}

func TestAesRoundTrip(t *testing.T) {
	for _, size := range []int{16, 24, 32} {
		for _, encoding := range []string{"default", EncodingTypebase16, EncodingTypebase64, "rawurl"} {
			t.Run(fmt.Sprintf("key_%d/%s", size, encoding), func(t *testing.T) {
				var options []AesOptionFunc
				if encoding == EncodingTypebase16 || encoding == EncodingTypebase64 {
					options = append(options, WithEncodingType(encoding))
				}
				utils, err := NewAesUtils(strings.Repeat("k", size), options...)
				if err != nil {
					t.Fatal(err)
				}
				encrypt, decrypt := utils.EncryptData, utils.DecryptData
				if encoding == "rawurl" {
					encrypt, decrypt = utils.EncryptDataRawURL, utils.DecryptDataRawURL
				}
				for _, plain := range []string{"", "hello AES", strings.Repeat("a", 16), "中文日志与多分组内容：" + strings.Repeat("abc", 20)} {
					encrypted, err := encrypt(plain)
					if err != nil {
						t.Fatal(err)
					}
					var ciphertext []byte
					switch encoding {
					case EncodingTypebase16:
						ciphertext, err = hex.DecodeString(encrypted)
					case "rawurl":
						if strings.ContainsAny(encrypted, "+/=") {
							t.Fatalf("not raw URL encoding: %q", encrypted)
						}
						ciphertext, err = base64.RawURLEncoding.DecodeString(encrypted)
					default:
						ciphertext, err = base64.StdEncoding.DecodeString(encrypted)
					}
					wantLength := (len(plain)/aes.BlockSize + 1) * aes.BlockSize
					if err != nil || len(ciphertext) != wantLength {
						t.Fatalf("invalid encoding or padding length: bytes=%d want=%d err=%v", len(ciphertext), wantLength, err)
					}
					decoded, err := decrypt(encrypted)
					if err != nil || decoded != plain {
						t.Fatalf("round trip: got %q want %q err=%v", decoded, plain, err)
					}
					t.Logf("原文=%q，密文=%s，解密结果=%q", plain, encrypted, decoded)
				}
			})
		}
	}
}

func TestAesDecryptInvalidInput(t *testing.T) {
	utils, err := NewAesUtils(strings.Repeat("k", 16))
	if err != nil {
		t.Fatal(err)
	}
	hexUtils, err := NewAesUtils(strings.Repeat("k", 16), WithEncodingType(EncodingTypebase16))
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name    string
		decrypt func(string) (string, error)
		short   string
	}{
		{name: "base64", decrypt: utils.DecryptData, short: base64.StdEncoding.EncodeToString([]byte("short"))},
		{name: "base16", decrypt: hexUtils.DecryptData, short: hex.EncodeToString([]byte("short"))},
		{name: "rawurl", decrypt: utils.DecryptDataRawURL, short: base64.RawURLEncoding.EncodeToString([]byte("short"))},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, input := range []string{"", "%%%", tc.short} {
				decoded, err := tc.decrypt(input)
				if err == nil || decoded != "" {
					t.Fatalf("invalid input accepted: %q => %q, %v", input, decoded, err)
				}
				want := "invalid ciphertext length"
				if input == "%%%" {
					want = "invalid encrypted data"
				}
				if err.Error() != want {
					t.Fatalf("want %q, got %v", want, err)
				}
				t.Logf("输入=%q，解密结果=%q，预期错误=%v", input, decoded, err)
			}
		})
	}
}
