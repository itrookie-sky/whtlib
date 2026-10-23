package tkhttp

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
)

type readFunc func(data []byte) (n int, err error)

func (f readFunc) Read(data []byte) (n int, err error) {
	return f(data)
}

type trackedBody struct {
	reader io.Reader
	closed atomic.Int32
	read   atomic.Int64
}

func (body *trackedBody) Read(data []byte) (n int, err error) {
	n, err = body.reader.Read(data)
	body.read.Add(int64(n))
	return
}

func (body *trackedBody) Close() (err error) {
	body.closed.Add(1)
	return nil
}

func TestResponseLimitReadErrorAndClose(t *testing.T) {
	readFailure := errors.New("private read details")
	cases := []struct {
		name      string
		reader    io.Reader
		limit     int64
		body      string
		truncated bool
		want      error
		read      int64
	}{
		{name: "exact limit", reader: strings.NewReader("12345"), limit: 5, body: "12345", read: 5},
		{name: "over limit", reader: strings.NewReader(strings.Repeat("x", 1024)), limit: 5, body: "xxxxx", truncated: true, want: ErrResponseTooLarge, read: 6},
		{name: "partial read", reader: readFunc(func(data []byte) (n int, err error) { return copy(data, "123"), readFailure }), limit: 5, body: "123", want: readFailure, read: 3},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			body := &trackedBody{reader: tc.reader}
			native := nativeResponse(200, "")
			native.Body = body
			native.ContentLength = 1
			transport := roundTripFunc(func(request *http.Request) (response *http.Response, err error) { return native, nil })
			config := quietConfig()
			config.MaxResponseBytes = tc.limit
			client := mustClient(t, config, WithHTTPClient(&http.Client{Transport: transport}))
			response, err := client.Get(t.Context(), "resource")
			if !errors.Is(err, tc.want) || response == nil || string(response.Body) != tc.body || response.BodyTruncated != tc.truncated || body.closed.Load() != 1 || body.read.Load() != tc.read {
				t.Fatalf("response=%+v err=%v closed=%d read=%d", response, err, body.closed.Load(), body.read.Load())
			}
			native.Header.Set("X-Response", "changed")
			if response.Header.Get("X-Response") != "original" {
				t.Fatal("response headers were not copied")
			}
		})
	}
}

func TestStatusErrorsNeverRetry(t *testing.T) {
	for _, status := range []int{302, 400, 429, 500, 502, 503, 504} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			var calls atomic.Int32
			body := &trackedBody{reader: strings.NewReader("private response")}
			transport := roundTripFunc(func(request *http.Request) (response *http.Response, err error) {
				calls.Add(1)
				response = nativeResponse(status, "")
				response.Header.Set("Retry-After", "120")
				response.Header.Set("Location", "https://other.example.com")
				response.Body = body
				return
			})
			client := mustClient(t, quietConfig(), WithHTTPClient(&http.Client{Transport: transport}))
			response, err := client.Get(t.Context(), "resource")
			statusError, ok := errors.AsType[*StatusError](err)
			if !ok || statusError.StatusCode != status || response.StatusCode != status || string(response.Body) != "private response" || calls.Load() != 1 || body.closed.Load() != 1 || strings.Contains(err.Error(), "private") {
				t.Fatalf("response=%v err=%v calls=%d closed=%d", response, err, calls.Load(), body.closed.Load())
			}
		})
	}
}

func TestReadErrorTakesPrecedenceOverStatus(t *testing.T) {
	cause := errors.New("read failure")
	transport := roundTripFunc(func(request *http.Request) (response *http.Response, err error) {
		response = nativeResponse(503, "")
		response.Body = &trackedBody{reader: readFunc(func(data []byte) (n int, err error) { return 0, cause })}
		return
	})
	client := mustClient(t, quietConfig(), WithHTTPClient(&http.Client{Transport: transport}))
	response, err := client.Get(t.Context(), "resource")
	failure, ok := errors.AsType[*RequestError](err)
	if !ok || failure.Phase != "read" || !errors.Is(err, cause) || response.StatusCode != 503 {
		t.Fatalf("response=%v err=%v", response, err)
	}
}

func TestDecodeRulesAndResponsePreservation(t *testing.T) {
	cases := []struct {
		name    string
		method  string
		status  int
		body    string
		wantErr bool
		want    int
	}{
		{name: "valid", method: "GET", status: 200, body: `{"id":2}`, want: 2},
		{name: "trailing", method: "GET", status: 200, body: `{"id":2} {}`, wantErr: true},
		{name: "invalid", method: "GET", status: 200, body: `bad`, wantErr: true},
		{name: "empty", method: "GET", status: 200, wantErr: true},
		{name: "head", method: "HEAD", status: 200, body: "bad", want: 1},
		{name: "no content", method: "GET", status: 204, want: 1},
		{name: "reset content", method: "GET", status: 205, want: 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			body := &trackedBody{reader: strings.NewReader(tc.body)}
			transport := roundTripFunc(func(request *http.Request) (response *http.Response, err error) {
				response = nativeResponse(tc.status, "")
				response.Body = body
				return
			})
			client := mustClient(t, quietConfig(), WithHTTPClient(&http.Client{Transport: transport}))
			result := struct {
				ID int `json:"id"`
			}{ID: 1}
			response, err := client.Do(t.Context(), tc.method, "resource", RequestConfig{Result: &result})
			if (err != nil) != tc.wantErr || response == nil || response.StatusCode != tc.status || body.closed.Load() != 1 {
				t.Fatalf("response=%v err=%v closed=%d", response, err, body.closed.Load())
			}
			if !tc.wantErr && result.ID != tc.want {
				t.Fatalf("decoded id=%d", result.ID)
			}
			if tc.wantErr {
				failure, ok := errors.AsType[*RequestError](err)
				if !ok || failure.Phase != "decode" {
					t.Fatal(err)
				}
			}
		})
	}
}

func TestTransportAndHookErrorsPreserveCause(t *testing.T) {
	cause := errors.New("https://api.example.com/?secret=credential")
	var calls atomic.Int32
	transport := roundTripFunc(func(request *http.Request) (response *http.Response, err error) {
		calls.Add(1)
		return nil, cause
	})
	client := mustClient(t, quietConfig(), WithHTTPClient(&http.Client{Transport: transport}))
	response, err := client.Get(t.Context(), "resource?secret=credential")
	if response != nil || !errors.Is(err, cause) || strings.Contains(err.Error(), "secret") || calls.Load() != 1 {
		t.Fatalf("response=%v err=%v calls=%d", response, err, calls.Load())
	}
	client = mustClient(t, quietConfig(), WithHTTPClient(&http.Client{Transport: transport}), WithRequestHook(func(ctx context.Context, info RequestInfo) (headers http.Header, err error) { return nil, cause }))
	_, err = client.Get(t.Context(), "resource")
	if !errors.Is(err, ErrRequestHook) || !errors.Is(err, cause) || strings.Contains(err.Error(), "secret") || calls.Load() != 1 {
		t.Fatalf("hook failure not preserved or sent request: %v", err)
	}
	for _, key := range []string{"Host", "Content-Length", "Transfer-Encoding", "Trailer", "Invalid Header"} {
		client = mustClient(t, quietConfig(), WithHTTPClient(&http.Client{Transport: transport}), WithRequestHook(func(ctx context.Context, info RequestInfo) (headers http.Header, err error) {
			return http.Header{key: {"private"}}, nil
		}))
		_, err = client.Get(t.Context(), "resource")
		if !errors.Is(err, ErrRequestHook) || calls.Load() != 1 {
			t.Fatalf("invalid hook header %s was sent: %v", key, err)
		}
	}
}
