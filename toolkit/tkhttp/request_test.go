package tkhttp

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type countedJSON struct {
	calls *atomic.Int32
	data  string
}

func (value countedJSON) MarshalJSON() (data []byte, err error) {
	value.calls.Add(1)
	return []byte(value.data), nil
}

func TestRequestEncodingHookAndSnapshots(t *testing.T) {
	var encodes, hooks, calls atomic.Int32
	headers := http.Header{"X-Default": {"original"}, "X-Override": {"default"}}
	query := url.Values{"page": {"2", "3"}}
	requestHeaders := http.Header{"x-override": {"request"}}
	config := quietConfig()
	config.DefaultHeader = headers
	transport := roundTripFunc(func(request *http.Request) (response *http.Response, err error) {
		calls.Add(1)
		data, err := io.ReadAll(request.Body)
		if err != nil {
			return
		}
		digest := sha256.Sum256(data)
		if string(data) != `{"name":"device"}` || request.Header.Get("X-Signature") != hex.EncodeToString(digest[:]) || request.Header.Get("X-Default") != "original" || request.Header.Get("X-Override") != "request" || request.Header.Get("Content-Type") != "application/json" || request.Header.Get("X-Mutated") != "" {
			t.Errorf("incorrect request body or headers: %s %v", data, request.Header)
		}
		if request.Method != "POST" || request.URL.Path != "/v1/devices" || !reflect.DeepEqual(request.URL.Query()["page"], []string{"2", "3"}) || request.URL.Query().Get("keep") != "yes" {
			t.Errorf("incorrect URL or method: %s %s", request.Method, request.URL)
		}
		request.Header.Set("X-Default", "transport mutation")
		return nativeResponse(200, `{"id":123}`), nil
	})
	client := mustClient(t, config,
		WithHTTPClient(&http.Client{Transport: transport}),
		WithRequestHook(func(ctx context.Context, info RequestInfo) (headers http.Header, err error) {
			hooks.Add(1)
			digest := sha256.Sum256(info.Body)
			info.Body[0] = 'x'
			info.Header.Set("X-Mutated", "secret")
			return http.Header{"X-Signature": {hex.EncodeToString(digest[:])}}, nil
		}),
	)
	headers.Set("X-Default", "external mutation")
	var result map[string]any
	response, err := client.Post(t.Context(), "devices?page=old&keep=yes", RequestConfig{
		Header: requestHeaders, Query: query, JSON: countedJSON{calls: &encodes, data: `{"name":"device"}`}, Result: &result,
	})
	if err != nil || response.StatusCode != 200 || result["id"] != float64(123) || encodes.Load() != 1 || hooks.Load() != 1 || calls.Load() != 1 {
		t.Fatalf("response=%v result=%v encodes/hooks/calls=%d/%d/%d err=%v", response, result, encodes.Load(), hooks.Load(), calls.Load(), err)
	}
	if !reflect.DeepEqual(requestHeaders, http.Header{"x-override": {"request"}}) || query.Get("page") != "2" || client.config.DefaultHeader.Get("X-Default") != "original" {
		t.Fatal("request or client defaults were mutated")
	}
}

func TestBodyFormatsAndExplicitContentType(t *testing.T) {
	cases := []struct {
		name        string
		config      RequestConfig
		body        string
		contentType string
	}{
		{name: "omitted"},
		{name: "raw", config: RequestConfig{Body: []byte("raw")}, body: "raw"},
		{name: "empty raw", config: RequestConfig{Body: []byte{}}},
		{name: "JSON", config: RequestConfig{JSON: map[string]int{"x": 1}}, body: `{"x":1}`, contentType: "application/json"},
		{name: "JSON null", config: RequestConfig{JSON: json.RawMessage("null")}, body: "null", contentType: "application/json"},
		{name: "typed nil JSON", config: RequestConfig{JSON: (*string)(nil)}, body: "null", contentType: "application/json"},
		{name: "form", config: RequestConfig{Form: url.Values{"x": {"1", "2"}}}, body: "x=1&x=2", contentType: "application/x-www-form-urlencoded"},
		{name: "empty form", config: RequestConfig{Form: url.Values{}}, contentType: "application/x-www-form-urlencoded"},
		{name: "explicit type", config: RequestConfig{JSON: 1, Header: http.Header{"content-type": {"custom/type"}}}, body: "1", contentType: "custom/type"},
		{name: "explicit no type", config: RequestConfig{JSON: 1, Header: http.Header{"content-type": nil}}, body: "1"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			transport := roundTripFunc(func(request *http.Request) (response *http.Response, err error) {
				data, err := io.ReadAll(request.Body)
				if err != nil {
					return
				}
				if string(data) != tc.body || request.Header.Get("Content-Type") != tc.contentType {
					t.Errorf("body=%q contentType=%q", data, request.Header.Get("Content-Type"))
				}
				return nativeResponse(200, ""), nil
			})
			client := mustClient(t, quietConfig(), WithHTTPClient(&http.Client{Transport: transport}))
			_, err := client.Post(t.Context(), "resource", tc.config)
			if err != nil {
				t.Fatal(err)
			}
		})
	}
}

type nilContext struct{ context.Context }

func TestInvalidRequestsDoNotReachHookOrNetwork(t *testing.T) {
	var calls, hooks atomic.Int32
	transport := roundTripFunc(func(request *http.Request) (response *http.Response, err error) {
		calls.Add(1)
		return nativeResponse(200, "ok"), nil
	})
	client := mustClient(t, quietConfig(), WithHTTPClient(&http.Client{Transport: transport}), WithRequestHook(func(ctx context.Context, info RequestInfo) (headers http.Header, err error) {
		hooks.Add(1)
		return nil, nil
	}))
	cases := []struct {
		name    string
		method  string
		target  string
		configs []RequestConfig
	}{
		{name: "method missing", target: "resource"},
		{name: "method invalid", method: "GET\nsecret", target: "resource"},
		{name: "target missing", method: "GET"},
		{name: "foreign origin", method: "GET", target: "https://other.example.com/secret"},
		{name: "scheme", method: "GET", target: "http://api.example.com/path"},
		{name: "port", method: "GET", target: "https://api.example.com:444/path"},
		{name: "network reference", method: "GET", target: "//api.example.com/path"},
		{name: "triple slash", method: "GET", target: "///secret"},
		{name: "userinfo", method: "GET", target: "https://secret@api.example.com/path"},
		{name: "fragment", method: "GET", target: "resource#"},
		{name: "bad escape", method: "GET", target: "%xx"},
		{name: "bad query", method: "GET", target: "resource?secret=%zz"},
		{name: "unmergeable query", method: "GET", target: "resource?raw=a;b", configs: []RequestConfig{{Query: url.Values{"new": {"value"}}}}},
		{name: "multiple configs", method: "GET", target: "resource", configs: []RequestConfig{{}, {}}},
		{name: "negative timeout", method: "GET", target: "resource", configs: []RequestConfig{{Timeout: -time.Second}}},
		{name: "nonpointer result", method: "GET", target: "resource", configs: []RequestConfig{{Result: 1}}},
		{name: "nil pointer result", method: "GET", target: "resource", configs: []RequestConfig{{Result: (*string)(nil)}}},
		{name: "conflicting body", method: "POST", target: "resource", configs: []RequestConfig{{JSON: 1, Body: []byte{}}}},
		{name: "conflicting form", method: "POST", target: "resource", configs: []RequestConfig{{Form: url.Values{}, Body: []byte{}}}},
		{name: "bad header name", method: "GET", target: "resource", configs: []RequestConfig{{Header: http.Header{"Bad Name": {"secret"}}}}},
		{name: "bad header value", method: "GET", target: "resource", configs: []RequestConfig{{Header: http.Header{"Good": {"secret\r\n"}}}}},
		{name: "controlled header", method: "GET", target: "resource", configs: []RequestConfig{{Header: http.Header{"host": {"secret"}}}}},
		{name: "duplicate header", method: "GET", target: "resource", configs: []RequestConfig{{Header: http.Header{"x-id": {"1"}, "X-Id": {"2"}}}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			response, err := client.Do(t.Context(), tc.method, tc.target, tc.configs...)
			if response != nil || !errors.Is(err, ErrInvalidRequest) || strings.Contains(err.Error(), "secret") {
				t.Fatalf("response=%v err=%v", response, err)
			}
		})
	}
	for _, ctx := range []context.Context{nil, (*nilContext)(nil)} {
		_, err := client.Get(ctx, "resource")
		if !errors.Is(err, ErrInvalidRequest) {
			t.Fatal(err)
		}
	}
	_, err := client.Post(t.Context(), "resource", RequestConfig{JSON: make(chan int)})
	if err == nil {
		t.Fatal("JSON encoding failure ignored")
	}
	if hooks.Load() != 0 || calls.Load() != 0 {
		t.Fatalf("invalid request executed hooks/network: %d/%d", hooks.Load(), calls.Load())
	}
}

func TestResolveURLAndOptionalConfiguration(t *testing.T) {
	config := quietConfig()
	config.DefaultHeader = http.Header{"X-Default": {"yes"}}
	var calls int
	transport := roundTripFunc(func(request *http.Request) (response *http.Response, err error) {
		calls++
		if request.Header.Get("X-Default") != "yes" {
			t.Error("client defaults missing")
		}
		return nativeResponse(200, request.URL.String()), nil
	})
	client := mustClient(t, config, WithHTTPClient(&http.Client{Transport: transport}))
	cases := []struct {
		target string
		want   string
	}{
		{target: "orders", want: "https://api.example.com/v1/orders"},
		{target: "/orders", want: "https://api.example.com/orders"},
		{target: "../orders", want: "https://api.example.com/orders"},
		{target: "https://API.EXAMPLE.COM:443/orders", want: "https://API.EXAMPLE.COM:443/orders"},
		{target: "https://api.example.com:0443/orders", want: "https://api.example.com:0443/orders"},
		{target: "escaped%2Fpath?q=a%20b", want: "https://api.example.com/v1/escaped%2Fpath?q=a%20b"},
		{target: "orders?raw=a;b", want: "https://api.example.com/v1/orders?raw=a;b"},
	}
	for _, tc := range cases {
		omitted, err := client.Get(t.Context(), tc.target)
		if err != nil || string(omitted.Body) != tc.want {
			t.Fatalf("target=%s response=%v err=%v", tc.target, omitted, err)
		}
		zero, err := client.Get(t.Context(), tc.target, RequestConfig{})
		if err != nil || !bytes.Equal(zero.Body, omitted.Body) {
			t.Fatalf("zero config differs: %v", err)
		}
	}
	if calls != len(cases)*2 {
		t.Fatal("unexpected extra calls")
	}
}
