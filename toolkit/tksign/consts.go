package tksign

// Encoding 表示字符串入口使用的签名编码。
type Encoding string

const (
	EncodingBase64    Encoding = "base64"
	EncodingBase64URL Encoding = "base64url"
	EncodingHex       Encoding = "hex"
)
