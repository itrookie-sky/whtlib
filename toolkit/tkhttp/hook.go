package tkhttp

import (
	"bytes"
	"context"
	"net/http"
)

// RequestInfo 提供签名输入的快照，修改快照不改变待发请求。
type RequestInfo struct {
	Method string      // 本次请求方法。
	URL    string      // 完整发送地址，包含 query，仅供钩子使用。
	Header http.Header // 请求头副本，不包含 Jar 或 Transport 隐式添加的字段。
	Body   []byte      // 编码一次后的固定正文副本。
}

// RequestHook 返回本次请求的签名或鉴权头；每次调用最多执行一次。
// 应配合 context 及时返回，不可覆盖 Host、Content-Length、Transfer-Encoding 或 Trailer。
type RequestHook func(ctx context.Context, info RequestInfo) (headers http.Header, err error)

func (c *Client) applyHook(ctx context.Context, request *http.Request, body []byte) (err error) {
	if c.hook == nil {
		return
	}
	header, err := c.hook(ctx, RequestInfo{
		Method: request.Method,
		URL:    request.URL.String(),
		Header: request.Header.Clone(),
		Body:   bytes.Clone(body),
	})
	if err != nil {
		err = withErrorKind(ErrRequestHook, err)
		return
	}
	header, err = copyHeader(header)
	if err != nil {
		err = withErrorKind(ErrRequestHook, err)
		return
	}
	for key, values := range header {
		request.Header[key] = values
	}
	return
}
