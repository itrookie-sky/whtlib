package tkaes

import (
	"crypto/aes"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"testing"
)

// TestNewAesUtilsKeyLength 验证合法密钥长度、边界长度及中文密钥的字节长度校验。
func TestNewAesUtilsKeyLength(t *testing.T) {
	// 覆盖空密钥、三种合法长度及其相邻的非法长度。
	for _, size := range []int{0, 15, 16, 17, 23, 24, 25, 31, 32, 33} {
		t.Run(fmt.Sprintf("bytes_%d", size), func(t *testing.T) {
			utils, err := NewAesUtils(strings.Repeat("k", size))
			valid := size == 16 || size == 24 || size == 32
			if valid {
				if err != nil || utils == nil {
					t.Fatalf("valid key rejected: %v", err)
				}
			} else {
				// 非法密钥应返回空实例，并保留标准库错误类型和实际字节数。
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

// TestAesRoundTrip 验证三种密钥长度与各编码方式的加解密往返结果。
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
				// 覆盖空文本、短文本、恰好一个分组及包含中文的多分组文本。
				for _, plain := range []string{"", "hello AES", strings.Repeat("a", 16), "中文日志与多分组内容：" + strings.Repeat("abc", 20)} {
					encrypted, err := encrypt(plain)
					if err != nil {
						t.Fatal(err)
					}
					// 独立解码密文，确认输出编码符合所选模式。
					var ciphertext []byte
					switch encoding {
					case EncodingTypebase16:
						ciphertext, err = hex.DecodeString(encrypted)
					case "rawurl":
						// URL 安全的无填充编码不能包含标准 Base64 的特殊字符和填充符。
						if strings.ContainsAny(encrypted, "+/=") {
							t.Fatalf("not raw URL encoding: %q", encrypted)
						}
						ciphertext, err = base64.RawURLEncoding.DecodeString(encrypted)
					default:
						ciphertext, err = base64.StdEncoding.DecodeString(encrypted)
					}
					// 即使原文长度恰好为分组整数倍，也应追加一个完整填充分组。
					wantLength := (len(plain)/aes.BlockSize + 1) * aes.BlockSize
					if err != nil || len(ciphertext) != wantLength {
						t.Fatalf("invalid encoding or padding length: bytes=%d want=%d err=%v", len(ciphertext), wantLength, err)
					}
					// 解密结果必须逐字节还原原文，并通过测试日志展示结果。
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

// TestAesDecryptInvalidInput 验证各解码入口拒绝空密文、非法编码和非分组长度密文。
func TestAesDecryptInvalidInput(t *testing.T) {
	utils, err := NewAesUtils(strings.Repeat("k", 16))
	if err != nil {
		t.Fatal(err)
	}
	hexUtils, err := NewAesUtils(strings.Repeat("k", 16), WithEncodingType(EncodingTypebase16))
	if err != nil {
		t.Fatal(err)
	}
	// short 使用合法编码包装五字节数据，以区分编码错误和密文长度错误。
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
				// 非法编码、空密文和短密文统一按无效密文类别判断。
				if !errors.Is(err, ErrInvalidCiphertext) {
					t.Fatalf("want ErrInvalidCiphertext, got %v", err)
				}
				t.Logf("输入=%q，解密结果=%q，预期错误=%v", input, decoded, err)
			}
		})
	}
}
