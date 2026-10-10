package jsonx

import (
	"bytes"
	"encoding/json"
	"strings"
)

// MarshalNoEscapeHTML 将 v 编码为单行 JSON 字符串，不进行 HTML 字符转义。
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
