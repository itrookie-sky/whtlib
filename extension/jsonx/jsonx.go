package jsonx

import (
	"bytes"
	"encoding/json"
	"strings"
)

// MarshalNoEscapeHTML 关闭HTML转义
func MarshalNoEscapeHTML(v any) (str string, err error) {
	buffer := &bytes.Buffer{}
	encoder := json.NewEncoder(buffer)
	encoder.SetEscapeHTML(false)
	err = encoder.Encode(v)
	if err != nil {
		return
	}
	str = strings.TrimSpace(buffer.String())
	return
}

// MustMarshalNoEscapeHTML 关闭 HTML 转义，编码失败时直接 panic。
func MustMarshalNoEscapeHTML(v any) (str string) {
	str, err := MarshalNoEscapeHTML(v)
	if err != nil {
		panic(err)
	}
	return
}
