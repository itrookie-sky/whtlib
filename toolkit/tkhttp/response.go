package tkhttp

import (
	"encoding/json"
	"io"
	"net/http"
)

// Response 保存本次调用的响应快照，正文已读取且关闭。
type Response struct {
	StatusCode    int         // 本次 HTTP 状态码。
	Header        http.Header // 响应头的独立副本。
	Body          []byte      // 缓冲正文；超限时保留限额内前缀。
	BodyTruncated bool        // 是否因超过响应限额而截断。
}

func responseMetadata(native *http.Response) (response *Response) {
	return &Response{StatusCode: native.StatusCode, Header: native.Header.Clone()}
}

func readResponse(native *http.Response, limit int64) (response *Response, readBytes int, err error) {
	defer native.Body.Close()
	response = responseMetadata(native)
	body, err := io.ReadAll(io.LimitReader(native.Body, limit+1))
	readBytes = len(body)
	response.Body = body
	if int64(len(body)) > limit {
		response.Body = body[:limit:limit]
		response.BodyTruncated = true
		if err == nil {
			err = ErrResponseTooLarge
		}
	}
	return
}

func decodeResponse(response *Response, method string, result any) (err error) {
	if result == nil || method == http.MethodHead || response.StatusCode == http.StatusNoContent || response.StatusCode == http.StatusResetContent {
		return
	}
	return json.Unmarshal(response.Body, result)
}
