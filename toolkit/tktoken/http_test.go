package tktoken

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

func TestHTTPMiddlewareBearerAndExplicitQuery(t *testing.T) {
	manager := testToken(t, newMemoryStore(), false)
	issued, err := manager.GenToken(t.Context(), "user", map[string]string{"role": "reader"})
	if err != nil {
		t.Fatal(err)
	}
	next := http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		session, found := SessionFromContext(request.Context())
		if !found || session.UserKey != "user" {
			t.Error("下游缺少已验证会话")
		}
		writer.WriteHeader(http.StatusNoContent)
	})
	for _, test := range []struct {
		name    string
		options HTTPOptions
		header  string
		query   string
		status  int
	}{
		{name: "missing", status: http.StatusUnauthorized},
		{name: "bearer", header: "Bearer " + issued.Token, status: http.StatusNoContent},
		{name: "caseInsensitive", header: "bearer " + issued.Token, status: http.StatusNoContent},
		{name: "emptyBearer", header: "Bearer", status: http.StatusUnauthorized},
		{name: "queryDisabled", query: issued.Token, status: http.StatusUnauthorized},
		{name: "queryEnabled", query: issued.Token, options: HTTPOptions{AllowQueryToken: true}, status: http.StatusNoContent},
		{name: "invalidHeaderCannotFallback", header: "Basic malformed", query: issued.Token, options: HTTPOptions{AllowQueryToken: true}, status: http.StatusUnauthorized},
	} {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, "/private?token="+url.QueryEscape(test.query), nil)
			if test.header != "" {
				request.Header.Set("Authorization", test.header)
			}
			writer := httptest.NewRecorder()
			manager.Middleware(test.options)(next).ServeHTTP(writer, request)
			if writer.Code != test.status {
				t.Fatalf("HTTP 状态 = %d，预期 %d", writer.Code, test.status)
			}
			if writer.Code == http.StatusUnauthorized && writer.Header().Get("WWW-Authenticate") != "Bearer" {
				t.Fatal("认证失败缺少 Bearer 挑战头")
			}
		})
	}
}

func TestHTTPPathsAndFailureCallback(t *testing.T) {
	manager := testToken(t, newMemoryStore(), false)
	paths := []string{"/api/*"}
	failures := 0
	options := HTTPOptions{
		Paths: paths, ExcludePaths: []string{"/api/public/*"},
		ShouldAuthenticate: func(request *http.Request) (required bool) {
			return request.URL.Path != "/api/skip"
		},
		OnFailure: func(writer http.ResponseWriter, request *http.Request, err error) {
			if !errors.Is(err, ErrUnauthorized) {
				t.Error("缺少认证应返回 unauthorized")
			}
			failures++
			writer.WriteHeader(http.StatusForbidden)
		},
	}
	handler := manager.Middleware(options)(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.WriteHeader(http.StatusNoContent)
	}))
	paths[0] = "/modified"
	for _, test := range []struct {
		path   string
		status int
	}{
		{path: "/api/private", status: http.StatusForbidden},
		{path: "/api/public", status: http.StatusNoContent},
		{path: "/api/public/item", status: http.StatusNoContent},
		{path: "/api2/private", status: http.StatusNoContent},
		{path: "/outside", status: http.StatusNoContent},
		{path: "/api/skip", status: http.StatusNoContent},
	} {
		writer := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodGet, test.path, nil)
		handler.ServeHTTP(writer, request)
		if writer.Code != test.status {
			t.Fatalf("路径 %s 状态 = %d，预期 %d", test.path, writer.Code, test.status)
		}
	}
	if failures != 1 {
		t.Fatalf("失败回调次数 = %d", failures)
	}
	if !MatchPath("/api/", "/api") || MatchPath("/api", "/api/item") || !MatchPath("/*", "/") {
		t.Fatal("精确路径或根目录规则错误")
	}
}
