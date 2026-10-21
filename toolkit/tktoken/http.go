package tktoken

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"slices"
	"strings"
)

// HTTPOptions 配置独立的 HTTP 提取、路径规则和失败响应。
type HTTPOptions struct {
	// Paths 为空时检查所有路径；规则支持精确匹配及以 /* 结尾的目录前缀。
	Paths        []string
	ExcludePaths []string
	// AllowQueryToken 显式启用旧查询参数，默认关闭。
	AllowQueryToken bool
	QueryParameter  string
	// ShouldAuthenticate 在路径匹配之后执行；返回 false 时直接交给下游。
	ShouldAuthenticate func(request *http.Request) bool
	OnFailure          func(writer http.ResponseWriter, request *http.Request, err error)
}

type sessionContextKey struct{}

// ExtractToken 默认只读取 Bearer 头；存在无效头时不会降级读取查询参数。
func ExtractToken(request *http.Request, options HTTPOptions) (value string, err error) {
	header := request.Header.Get("Authorization")
	if header != "" {
		parts := strings.Fields(header)
		if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
			err = ErrUnauthorized
			return
		}
		value = parts[1]
		return
	}
	if options.AllowQueryToken {
		name := options.QueryParameter
		if name == "" {
			name = "token"
		}
		value = request.URL.Query().Get(name)
		if value != "" {
			return
		}
	}
	err = ErrUnauthorized
	return
}

// RequiresAuthentication 判断路径是否命中认证规则；排除规则优先。
func RequiresAuthentication(path string, options HTTPOptions) (required bool) {
	for _, pattern := range options.ExcludePaths {
		if MatchPath(pattern, path) {
			return
		}
	}
	if len(options.Paths) == 0 {
		required = true
		return
	}
	for _, pattern := range options.Paths {
		if MatchPath(pattern, path) {
			required = true
			return
		}
	}
	return
}

// MatchPath 支持精确路径或 /* 目录前缀，不将 /api2 匹配到 /api/*。
func MatchPath(pattern, path string) (matched bool) {
	path = strings.TrimSuffix(path, "/")
	prefix, wildcard := strings.CutSuffix(pattern, "/*")
	if wildcard {
		matched = path == prefix || strings.HasPrefix(path, prefix+"/")
		return
	}
	matched = path == strings.TrimSuffix(pattern, "/")
	return
}

// Middleware 返回标准 net/http 中间件，将验证后的会话写入请求上下文。
func (t *Token) Middleware(options HTTPOptions) (middleware func(http.Handler) http.Handler) {
	options.Paths = slices.Clone(options.Paths)
	options.ExcludePaths = slices.Clone(options.ExcludePaths)
	if options.OnFailure == nil {
		options.OnFailure = defaultFailure
	}
	middleware = func(next http.Handler) (handler http.Handler) {
		return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			if !RequiresAuthentication(request.URL.Path, options) {
				next.ServeHTTP(writer, request)
				return
			}
			if options.ShouldAuthenticate != nil && !options.ShouldAuthenticate(request) {
				next.ServeHTTP(writer, request)
				return
			}
			value, err := ExtractToken(request, options)
			if err != nil {
				options.OnFailure(writer, request, err)
				return
			}
			session, err := t.ValidToken(request.Context(), value)
			if err != nil {
				options.OnFailure(writer, request, err)
				return
			}
			ctx := context.WithValue(request.Context(), sessionContextKey{}, session)
			next.ServeHTTP(writer, request.WithContext(ctx))
		})
	}
	return
}

// SessionFromContext 返回中间件写入的会话副本。
func SessionFromContext(ctx context.Context) (session Session, found bool) {
	session, found = ctx.Value(sessionContextKey{}).(Session)
	session.Data = bytes.Clone(session.Data)
	return
}

func defaultFailure(writer http.ResponseWriter, _ *http.Request, err error) {
	if errors.Is(err, ErrUnauthorized) || errors.Is(err, ErrNotFound) {
		writer.Header().Set("WWW-Authenticate", "Bearer")
		http.Error(writer, "Unauthorized", http.StatusUnauthorized)
		return
	}
	http.Error(writer, "Internal Server Error", http.StatusInternalServerError)
}
