package tkhttp

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/textproto"
	"net/url"
	"reflect"
	"slices"
	"strconv"
	"strings"

	"github.com/itrookie-sky/whtlib/extension/reflectx"
)

func parseBaseURL(raw string) (base *url.URL, err error) {
	base, err = url.Parse(raw)
	if err != nil || !validHTTPURL(base) || base.User != nil || base.RawQuery != "" || base.ForceQuery || strings.Contains(raw, "#") {
		err = invalidConfig("BaseURL requires an absolute HTTP(S) URL without userinfo, query or fragment")
		return
	}
	base.Scheme = strings.ToLower(base.Scheme)
	return
}

func validHTTPURL(value *url.URL) (valid bool) {
	if value == nil || value.Opaque != "" || value.Host == "" || value.Hostname() == "" {
		return false
	}
	if !strings.EqualFold(value.Scheme, "http") && !strings.EqualFold(value.Scheme, "https") {
		return false
	}
	port := value.Port()
	if port != "" {
		number, err := strconv.Atoi(port)
		if err != nil || number < 1 || number > 65535 {
			return false
		}
	}
	return true
}

func effectivePort(value *url.URL) (port string) {
	if value.Port() != "" {
		number, _ := strconv.Atoi(value.Port()) // 调用前已由 validHTTPURL 校验。
		return strconv.Itoa(number)
	}
	if strings.EqualFold(value.Scheme, "https") {
		return "443"
	}
	return "80"
}

func (c *Client) resolveURL(target string, query url.Values) (resolved *url.URL, err error) {
	if target == "" || strings.Contains(target, "#") {
		err = invalidRequest("target must be nonempty and contain no fragment")
		return
	}
	reference, parseErr := url.Parse(target)
	if parseErr != nil || reference.User != nil || reference.Opaque != "" || (reference.Scheme == "" && reference.Host != "") || strings.HasPrefix(target, "//") {
		err = invalidRequest("target URL is invalid")
		return
	}
	resolved = c.baseURL.ResolveReference(reference)
	if !validHTTPURL(resolved) || !strings.EqualFold(resolved.Scheme, c.baseURL.Scheme) || !strings.EqualFold(resolved.Hostname(), c.baseURL.Hostname()) || effectivePort(resolved) != effectivePort(c.baseURL) {
		err = invalidRequest("target must have the same origin as BaseURL")
		return
	}
	resolved.Scheme = c.baseURL.Scheme
	_, parseErr = url.QueryUnescape(resolved.RawQuery)
	if parseErr != nil {
		err = invalidRequest("target query is invalid")
		return
	}
	if query != nil {
		values, queryErr := url.ParseQuery(resolved.RawQuery)
		if queryErr != nil {
			err = invalidRequest("target query cannot be merged")
			return
		}
		for key, entries := range query {
			values[key] = slices.Clone(entries)
		}
		resolved.RawQuery = values.Encode()
	}
	return
}

func validToken(token string) (valid bool) {
	if token == "" {
		return false
	}
	for i := range len(token) {
		b := token[i]
		if b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' || b >= '0' && b <= '9' || strings.IndexByte("!#$%&'*+-.^_`|~", b) >= 0 {
			continue
		}
		return false
	}
	return true
}

func copyHeader(source http.Header) (header http.Header, err error) {
	header = make(http.Header, len(source))
	for key, values := range source {
		if !validToken(key) {
			err = invalidRequest("header name is invalid")
			return
		}
		canonical := textproto.CanonicalMIMEHeaderKey(key)
		switch canonical {
		case "Host", "Content-Length", "Transfer-Encoding", "Trailer":
			err = invalidRequest("header is controlled by the HTTP transport")
			return
		}
		if _, exists := header[canonical]; exists {
			err = invalidRequest("header contains duplicate names with different casing")
			return
		}
		for _, value := range values {
			for i := range len(value) {
				if value[i] == 0x7f || value[i] < 0x20 && value[i] != '\t' {
					err = invalidRequest("header value is invalid")
					return
				}
			}
		}
		header[canonical] = slices.Clone(values)
	}
	return
}

func selectConfig(configs []RequestConfig) (config RequestConfig, err error) {
	if len(configs) > 1 {
		err = invalidRequest("at most one RequestConfig is allowed")
		return
	}
	if len(configs) == 1 {
		config = configs[0]
	}
	if config.Timeout < -1 {
		err = invalidRequest("Timeout must be positive, zero or -1")
		return
	}
	if config.Result != nil && (reflect.TypeOf(config.Result).Kind() != reflect.Pointer || reflectx.IsNil(config.Result)) {
		err = invalidRequest("Result must be a non-nil pointer")
		return
	}
	return
}

func encodeBody(config RequestConfig) (body []byte, contentType string, err error) {
	count := 0
	for _, present := range []bool{config.JSON != nil, config.Form != nil, config.Body != nil} {
		if present {
			count++
		}
	}
	if count > 1 {
		err = invalidRequest("JSON, Form and Body are mutually exclusive")
		return
	}
	switch {
	case config.JSON != nil:
		body, err = json.Marshal(config.JSON)
		if err != nil {
			err = withErrorKind(ErrRequestEncode, err)
			return
		}
		contentType = "application/json"
	case config.Form != nil:
		form := make(url.Values, len(config.Form))
		for key, values := range config.Form {
			form[key] = slices.Clone(values)
		}
		body = []byte(form.Encode())
		contentType = "application/x-www-form-urlencoded"
	default:
		body = bytes.Clone(config.Body)
	}
	return
}

func (c *Client) prepare(ctx context.Context, method, target string, config RequestConfig) (request *http.Request, body []byte, err error) {
	if !validToken(method) {
		err = invalidRequest("method must be a valid HTTP token")
		return
	}
	resolved, err := c.resolveURL(target, config.Query)
	if err != nil {
		return
	}
	header, err := copyHeader(config.Header)
	if err != nil {
		return
	}
	merged := c.config.DefaultHeader.Clone()
	for key, values := range header {
		merged[key] = values
	}
	body, contentType, err := encodeBody(config)
	if err != nil {
		return
	}
	if _, explicit := merged["Content-Type"]; !explicit && contentType != "" {
		merged.Set("Content-Type", contentType)
	}
	request, err = http.NewRequestWithContext(ctx, method, resolved.String(), bytes.NewReader(body))
	if err != nil {
		err = invalidRequest("request cannot be constructed")
		return
	}
	request.Header = merged
	return
}

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
