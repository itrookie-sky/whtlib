package tkhttp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/itrookie-sky/whtlib/extension/errx"
)

type nilHTTPContext struct{ context.Context }

func TestMiddlewareObservesCompleteErrorsAndStatus(t *testing.T) {
	cause := errors.New("private failure")
	cases := []struct {
		name      string
		configs   []RequestConfig
		badCtx    bool
		typedNil  bool
		cancel    bool
		hook      bool
		transport bool
		read      bool
		status    int
		body      string
		limit     int64
		want      error
	}{
		{name: "success", status: 200, body: `{}`},
		{name: "invalid parameters", configs: []RequestConfig{{}, {}}, want: ErrInvalidRequest},
		{name: "nil context", badCtx: true, want: ErrInvalidRequest},
		{name: "typed nil context", typedNil: true, want: ErrInvalidRequest},
		{name: "request encode", configs: []RequestConfig{{JSON: make(chan int)}}, want: ErrRequestEncode},
		{name: "canceled", cancel: true, want: context.Canceled},
		{name: "hook", hook: true, want: ErrRequestHook},
		{name: "transport", transport: true, want: ErrTransport},
		{name: "read", read: true, status: 206, want: ErrResponseRead},
		{name: "limit", status: 200, body: "123456", limit: 3, want: ErrResponseTooLarge},
		{name: "status", status: 503, body: "private body", want: ErrHTTPStatus},
		{name: "decode", status: 200, body: "invalid", configs: []RequestConfig{{Result: new(map[string]any)}}, want: ErrResponseDecode},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			config := quietConfig()
			config.MaxResponseBytes = tc.limit
			var observed *Response
			var observedErr error
			var body *trackedBody
			calls := 0
			transport := roundTripFunc(func(request *http.Request) (response *http.Response, err error) {
				calls++
				if tc.transport {
					return nil, cause
				}
				response = nativeResponse(tc.status, tc.body)
				var reader io.Reader = strings.NewReader(tc.body)
				if tc.read {
					reader = readFunc(func(data []byte) (n int, err error) { return copy(data, "a"), cause })
				}
				body = &trackedBody{reader: reader}
				response.Body = body
				return
			})
			observer := func(next Handler) (handler Handler) {
				return func(ctx context.Context, method, target string, configs ...RequestConfig) (response *Response, err error) {
					response, err = next(ctx, method, target, configs...)
					observed, observedErr = response, err
					if err != nil {
						err = fmt.Errorf("observed: %w", err)
					}
					return
				}
			}
			opts := []Option{WithHTTPClient(&http.Client{Transport: transport}), WithMiddleware(observer)}
			if tc.hook {
				opts = append(opts, WithRequestHook(func(ctx context.Context, info RequestInfo) (headers http.Header, err error) { return nil, cause }))
			}
			client := mustClient(t, config, opts...)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			var supplied context.Context = ctx
			if tc.cancel {
				cancel()
			}
			if tc.badCtx {
				supplied = nil
			}
			if tc.typedNil {
				supplied = (*nilHTTPContext)(nil)
			}
			response, err := client.Get(supplied, "resource", tc.configs...)
			if response != observed || !errors.Is(err, tc.want) || !errors.Is(observedErr, tc.want) {
				t.Fatalf("response=%+v err=%v observed=%v", response, err, observedErr)
			}
			if tc.status == 0 && response != nil || tc.status != 0 && (response == nil || response.StatusCode != tc.status) {
				t.Fatalf("lost HTTP status: %+v", response)
			}
			if body != nil && body.closed.Load() != 1 {
				t.Fatal("response body was not closed exactly once")
			}
			if tc.hook || tc.transport || tc.read {
				if !errors.Is(err, cause) {
					t.Fatal("original cause lost: ", err)
				}
			}
			if tc.status == 0 && !tc.transport && calls != 0 {
				t.Fatal("invalid request reached network")
			}
			if tc.want == ErrResponseTooLarge && !errors.Is(err, ErrResponseRead) {
				t.Fatal("limit missing read category")
			}
			if tc.want == ErrRequestEncode {
				_, ok := errors.AsType[*json.UnsupportedTypeError](err)
				if !ok {
					t.Fatal("original encode error type lost")
				}
			}
			if tc.want == ErrResponseDecode {
				_, ok := errors.AsType[*json.SyntaxError](err)
				if !ok {
					t.Fatal("original decode error type lost")
				}
			}
			if tc.want == ErrHTTPStatus {
				status, ok := errors.AsType[*StatusError](err)
				if !ok || status.StatusCode != tc.status {
					t.Fatal("typed HTTP status lost")
				}
			}
		})
	}
}

func TestReadLimitPreservesConcurrentReadError(t *testing.T) {
	cause := errors.New("read failed")
	transport := roundTripFunc(func(request *http.Request) (response *http.Response, err error) {
		response = nativeResponse(502, "")
		response.Body = &trackedBody{reader: readFunc(func(data []byte) (n int, err error) { return copy(data, "1234"), cause })}
		return
	})
	config := quietConfig()
	config.MaxResponseBytes = 3
	client := mustClient(t, config, WithHTTPClient(&http.Client{Transport: transport}))
	response, err := client.Get(t.Context(), "resource")
	if !errors.Is(err, ErrResponseRead) || !errors.Is(err, ErrResponseTooLarge) || !errors.Is(err, cause) || response.StatusCode != 502 {
		t.Fatalf("response=%+v err=%v", response, err)
	}
}

func TestErrxPreservesErrorChain(t *testing.T) {
	status := &StatusError{StatusCode: 503}
	request := &RequestError{Phase: PhaseTransport, Err: errors.Join(ErrTransport, context.DeadlineExceeded, status)}
	wrapped := errx.WithStack(errx.WithFields(request, map[string]any{"operation": "fetch"}))
	if wrapped.Error() != request.Error() || strings.Contains(wrapped.Error(), "fetch") {
		t.Fatalf("错误文本被元数据改变: %s", wrapped)
	}
	for _, target := range []error{request, status, ErrTransport, ErrHTTPStatus, context.DeadlineExceeded} {
		if !errors.Is(wrapped, target) {
			t.Errorf("错误类别丢失: %v", target)
		}
	}
	gotRequest, ok := errors.AsType[*RequestError](wrapped)
	if !ok || gotRequest != request {
		t.Fatal("RequestError 类型丢失")
	}
	gotStatus, ok := errors.AsType[*StatusError](wrapped)
	if !ok || gotStatus.StatusCode != 503 {
		t.Fatal("StatusError 状态码丢失")
	}
}
