// Package tkhttp 提供可注入标准库连接池和累计日志的单次 HTTP 调用。
package tkhttp

import (
	"math"
	"net/http"
	"net/url"
	"reflect"
	"time"
)

// Config 配置客户端基本参数；依赖通过 With 选项注入。
type Config struct {
	BaseURL          string        // 必填 HTTP(S) 绝对地址，不允许 userinfo、query 或 fragment。
	DefaultHeader    http.Header   // 默认请求头，New 复制后固定。
	Timeout          time.Duration // 0 默认 10s，精确 time.Duration(-1) 不添加组件超时。
	MaxResponseBytes int64         // 0 默认 8 MiB，正数限制实际读取的响应字节。
	DisableLog       bool          // true 关闭汇总日志；默认必须注入有效 LogAcc。
}

// RequestConfig 按需覆盖当前请求；省略或零值均沿用客户端默认值。
type RequestConfig struct {
	Header    http.Header    // 按字段覆盖默认请求头，不修改传入容器。
	Query     url.Values     // 合并 URL query，同名键整体替换并保留多值。
	JSON      any            // nil 表示未设置；其他输入按 JSON 编码一次。
	Form      url.Values     // nil 表示未设置；空集合仍为显式表单体。
	Body      []byte         // nil 表示未设置；空切片仍为显式原始请求体。
	Result    any            // 可选 JSON 解码目标，必须为非 nil 指针。
	Timeout   time.Duration  // 0 继承 Client，正数覆盖，精确 -1 禁用组件超时。
	LogFields map[string]any // 显式非敏感业务字段，放入日志 biz 组。
}

func normalizeConfig(config Config) (normalized Config, baseURL *url.URL, err error) {
	normalized = config
	baseURL, err = parseBaseURL(config.BaseURL)
	if err != nil {
		return
	}
	if config.Timeout < -1 {
		err = invalidConfig("Timeout must be positive, zero or -1")
		return
	}
	if normalized.Timeout == 0 {
		normalized.Timeout = 10 * time.Second
	}
	if config.MaxResponseBytes < 0 || config.MaxResponseBytes == math.MaxInt64 {
		err = invalidConfig("MaxResponseBytes must be positive and allow one extra byte")
		return
	}
	if normalized.MaxResponseBytes == 0 {
		normalized.MaxResponseBytes = 8 << 20
	}
	normalized.DefaultHeader, err = copyHeader(config.DefaultHeader)
	if err != nil {
		err = invalidConfig("DefaultHeader contains invalid or transport-controlled fields")
		return
	}
	return
}

func isNil(value any) (nilValue bool) {
	if value == nil {
		return true
	}
	ref := reflect.ValueOf(value)
	switch ref.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return ref.IsNil()
	}
	return false
}
