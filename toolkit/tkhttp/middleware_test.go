package tkhttp

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/itrookie-sky/whtlib/adapter"
)

func TestMiddlewareOrderOptionsAndSnapshots(t *testing.T) {
	var events []string
	constructs := 0
	middleware := func(name string) (result Middleware) {
		return func(next Handler) (handler Handler) {
			constructs++
			return func(ctx context.Context, method, target string, configs ...RequestConfig) (response *Response, err error) {
				events = append(events, name+" before")
				response, err = next(ctx, method, target, configs...)
				events = append(events, name+" after")
				return
			}
		}
	}
	selected := []Middleware{middleware("A")}
	option := WithMiddleware(selected...)
	selected[0] = nil
	transport := roundTripFunc(func(request *http.Request) (response *http.Response, err error) {
		events = append(events, "core")
		return nativeResponse(200, "ok"), nil
	})
	client := mustClient(t, quietConfig(), WithHTTPClient(&http.Client{Transport: transport}), option, WithMiddleware(), WithMiddleware(middleware("B")))
	if constructs != 0 {
		t.Fatal("New executed middleware")
	}
	for range 2 {
		events = nil
		_, err := client.Get(t.Context(), "resource")
		if err != nil || !slices.Equal(events, []string{"A before", "B before", "core", "B after", "A after"}) {
			t.Fatalf("events=%v err=%v", events, err)
		}
	}
	if constructs != 4 {
		t.Fatal("middleware wrappers were shared across requests")
	}
	invalid, err := New(quietConfig(), WithMiddleware(nil))
	if invalid != nil || !errors.Is(err, ErrInvalidConfig) {
		t.Fatal("nil middleware accepted")
	}
}

func TestMiddlewareShortCircuitAndInvalidResults(t *testing.T) {
	cause := errors.New("private rejection")
	cases := []struct {
		name  string
		build Middleware
		calls int32
		want  error
	}{
		{name: "reject", want: cause, build: func(next Handler) (handler Handler) {
			return func(ctx context.Context, method, target string, configs ...RequestConfig) (response *Response, err error) {
				return nil, cause
			}
		}},
		{name: "synthetic response", build: func(next Handler) (handler Handler) {
			return func(ctx context.Context, method, target string, configs ...RequestConfig) (response *Response, err error) {
				return &Response{StatusCode: 202}, nil
			}
		}},
		{name: "nil handler", want: ErrMiddleware, build: func(next Handler) (handler Handler) { return nil }},
		{name: "nil result", want: ErrMiddleware, build: func(next Handler) (handler Handler) {
			return func(ctx context.Context, method, target string, configs ...RequestConfig) (response *Response, err error) {
				return nil, nil
			}
		}},
		{name: "duplicate next ignored", calls: 1, want: ErrMiddleware, build: func(next Handler) (handler Handler) {
			return func(ctx context.Context, method, target string, configs ...RequestConfig) (response *Response, err error) {
				response, err = next(ctx, method, target, configs...)
				_, _ = next(ctx, method, target, configs...)
				return
			}
		}},
		{name: "next during construction", want: ErrMiddleware, build: func(next Handler) (handler Handler) {
			_, _ = next(t.Context(), "GET", "resource")
			return next
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var calls atomic.Int32
			transport := roundTripFunc(func(request *http.Request) (response *http.Response, err error) {
				calls.Add(1)
				return nativeResponse(200, "ok"), nil
			})
			acc := newRecordingAcc()
			config := quietConfig()
			config.DisableLog = false
			client := mustClient(t, config, WithHTTPClient(&http.Client{Transport: transport}), WithLogAcc(acc.newAcc), WithMiddleware(tc.build))
			response, err := client.Get(t.Context(), "resource")
			if !errors.Is(err, tc.want) || calls.Load() != tc.calls || len(acc.sink.records) != 1 {
				t.Fatalf("response=%v err=%v calls=%d records=%d", response, err, calls.Load(), len(acc.sink.records))
			}
			httpFields := acc.sink.records[0].fields["http"].(map[string]any)
			if err != nil && (!errors.Is(err, ErrMiddleware) || httpFields["phase"] != "middleware") {
				t.Fatal("middleware error category or phase missing")
			}
			if err == nil && (response.StatusCode != 202 || httpFields["statusCode"] != 202 || httpFields["phase"] != "complete") {
				t.Fatal("synthetic HTTP status lost")
			}
		})
	}
}

func TestMiddlewareNextExpiresAndPanicPropagates(t *testing.T) {
	var saved Handler
	var calls atomic.Int32
	transport := roundTripFunc(func(request *http.Request) (response *http.Response, err error) {
		calls.Add(1)
		return nativeResponse(200, ""), nil
	})
	middleware := func(next Handler) (handler Handler) {
		saved = next
		return func(ctx context.Context, method, target string, configs ...RequestConfig) (response *Response, err error) {
			return &Response{StatusCode: 204}, nil
		}
	}
	client := mustClient(t, quietConfig(), WithHTTPClient(&http.Client{Transport: transport}), WithMiddleware(middleware))
	_, err := client.Get(t.Context(), "resource")
	if err != nil {
		t.Fatal(err)
	}
	_, err = saved(t.Context(), "GET", "resource")
	if !errors.Is(err, ErrMiddleware) || calls.Load() != 0 {
		t.Fatal("expired next reached network")
	}
	panicMiddleware := func(next Handler) (handler Handler) {
		saved = next
		return func(ctx context.Context, method, target string, configs ...RequestConfig) (response *Response, err error) {
			panic("expected")
		}
	}
	client = mustClient(t, quietConfig(), WithHTTPClient(&http.Client{Transport: transport}), WithMiddleware(panicMiddleware))
	func() {
		defer func() {
			if recover() != "expected" {
				t.Error("middleware panic did not propagate")
			}
		}()
		_, _ = client.Get(t.Context(), "resource")
	}()
	_, err = saved(t.Context(), "GET", "resource")
	if !errors.Is(err, ErrMiddleware) || calls.Load() != 0 {
		t.Fatal("panic left next open")
	}
}

func TestMiddlewareWaitsForStartedNext(t *testing.T) {
	entered := make(chan struct{})
	returning := make(chan struct{})
	release := make(chan struct{})
	cleanup := sync.OnceFunc(func() { close(release) })
	t.Cleanup(cleanup)
	transport := roundTripFunc(func(request *http.Request) (response *http.Response, err error) {
		close(entered)
		<-release
		return nativeResponse(204, ""), nil
	})
	middleware := func(next Handler) (handler Handler) {
		return func(ctx context.Context, method, target string, configs ...RequestConfig) (response *Response, err error) {
			go func() { _, _ = next(ctx, method, target, configs...) }()
			<-entered
			close(returning)
			return &Response{StatusCode: 202}, nil
		}
	}
	client := mustClient(t, quietConfig(), WithHTTPClient(&http.Client{Transport: transport}), WithMiddleware(middleware))
	finished := make(chan error, 1)
	go func() {
		_, err := client.Get(t.Context(), "resource")
		finished <- err
	}()
	<-returning
	select {
	case err := <-finished:
		t.Fatalf("call returned before running next stopped: %v", err)
	case <-time.After(20 * time.Millisecond):
	}
	cleanup()
	err := <-finished
	if !errors.Is(err, ErrMiddleware) {
		t.Fatal("asynchronous next was accepted")
	}
}

func TestInvalidLogFactoryDoesNotEnterMiddleware(t *testing.T) {
	acc := newRecordingAcc()
	acc.sink.failAt = 1
	calls := 0
	middleware := func(next Handler) (handler Handler) {
		calls++
		return next
	}
	config := quietConfig()
	config.DisableLog = false
	client := mustClient(t, config, WithLogAcc(acc.newAcc), WithMiddleware(middleware))
	_, err := client.Get(t.Context(), "resource")
	if !errors.Is(err, ErrLogAcc) || calls != 0 {
		t.Fatalf("invalid factory entered middleware: %v", err)
	}
}

func TestMiddlewareFinalLogResult(t *testing.T) {
	cause := errors.New("replacement")
	for _, consume := range []bool{true, false} {
		acc := newRecordingAcc()
		config := quietConfig()
		config.DisableLog = false
		transport := roundTripFunc(func(request *http.Request) (response *http.Response, err error) {
			return nativeResponse(503, "body"), nil
		})
		middleware := func(next Handler) (handler Handler) {
			return func(ctx context.Context, method, target string, configs ...RequestConfig) (response *Response, err error) {
				response, err = next(ctx, method, target, configs...)
				if consume {
					return response, nil
				}
				return response, cause
			}
		}
		client := mustClient(t, config, WithHTTPClient(&http.Client{Transport: transport}), WithLogAcc(acc.newAcc), WithMiddleware(middleware))
		response, err := client.Get(t.Context(), "resource")
		if response.StatusCode != 503 || len(acc.sink.records) != 1 || (err == nil) != consume {
			t.Fatalf("response=%v err=%v", response, err)
		}
		record := acc.sink.records[0]
		fields := record.fields["http"].(map[string]any)
		if consume && (record.level != "info" || fields["phase"] != "complete" || fields["errorKind"] != "") {
			t.Fatalf("stale failure log: %+v", record)
		}
		if !consume && (!errors.Is(err, cause) || record.level != "error" || fields["phase"] != "middleware" || fields["errorKind"] != "middleware") {
			t.Fatalf("replacement log missing: %+v", record)
		}
	}
}

func TestMiddlewareChangesReachSigningHook(t *testing.T) {
	headers := http.Header{"X-Test": {"before"}}
	middleware := func(next Handler) (handler Handler) {
		return func(ctx context.Context, method, target string, configs ...RequestConfig) (response *Response, err error) {
			config := configs[0]
			config.Header = config.Header.Clone()
			config.Header.Set("X-Test", "after")
			return next(ctx, "POST", "changed", config)
		}
	}
	checked := false
	hook := func(ctx context.Context, info RequestInfo) (headers http.Header, err error) {
		if info.Method != "POST" || info.URL != "https://api.example.com/v1/changed" || info.Header.Get("X-Test") != "after" {
			t.Errorf("hook saw stale request: %+v", info)
		}
		checked = true
		return http.Header{"X-Signature": {"signed"}}, nil
	}
	transport := roundTripFunc(func(request *http.Request) (response *http.Response, err error) {
		if !checked || request.Header.Get("X-Signature") != "signed" {
			t.Error("hook did not run after middleware")
		}
		return nativeResponse(204, ""), nil
	})
	client := mustClient(t, quietConfig(), WithHTTPClient(&http.Client{Transport: transport}), WithRequestHook(hook), WithMiddleware(middleware))
	_, err := client.Get(t.Context(), "original", RequestConfig{Header: headers})
	if err != nil || headers.Get("X-Test") != "before" {
		t.Fatal("middleware changed shared input: ", err)
	}
}

func TestMiddlewareViolationTakesPrecedenceOverHTTPStatus(t *testing.T) {
	acc := newRecordingAcc()
	config := quietConfig()
	config.DisableLog = false
	transport := roundTripFunc(func(request *http.Request) (response *http.Response, err error) { return nativeResponse(400, ""), nil })
	middleware := func(next Handler) (handler Handler) {
		return func(ctx context.Context, method, target string, configs ...RequestConfig) (response *Response, err error) {
			response, err = next(ctx, method, target, configs...)
			_, _ = next(ctx, method, target, configs...)
			return
		}
	}
	client := mustClient(t, config, WithHTTPClient(&http.Client{Transport: transport}), WithLogAcc(acc.newAcc), WithMiddleware(middleware))
	response, err := client.Get(t.Context(), "resource")
	if !errors.Is(err, ErrMiddleware) || !errors.Is(err, ErrHTTPStatus) || response.StatusCode != 400 {
		t.Fatalf("response=%v err=%v", response, err)
	}
	record := acc.sink.records[0]
	fields := record.fields["http"].(map[string]any)
	if record.level != "error" || fields["phase"] != "middleware" || fields["errorKind"] != "middleware" {
		t.Fatalf("middleware violation logged as ordinary HTTP failure: %+v", record)
	}
}

func TestMiddlewareConcurrentStateIsolation(t *testing.T) {
	acc := newRecordingAcc()
	config := quietConfig()
	config.DisableLog = false
	var constructs atomic.Int32
	middleware := func(next Handler) (handler Handler) {
		constructs.Add(1)
		calls := 0
		return func(ctx context.Context, method, target string, configs ...RequestConfig) (response *Response, err error) {
			calls++
			if calls != 1 {
				t.Error("request handler was shared")
			}
			return next(ctx, method, target, RequestConfig{LogFields: map[string]any{"id": target}})
		}
	}
	transport := roundTripFunc(func(request *http.Request) (response *http.Response, err error) { return nativeResponse(204, ""), nil })
	client := mustClient(t, config, WithHTTPClient(&http.Client{Transport: transport}), WithLogAcc(func() (instance adapter.LogAcc) { return acc.newAcc() }), WithMiddleware(middleware))
	var group sync.WaitGroup
	for index := range 30 {
		group.Go(func() {
			_, err := client.Get(t.Context(), strconv.Itoa(index))
			if err != nil {
				t.Error(err)
			}
		})
	}
	group.Wait()
	if constructs.Load() != 30 || len(acc.sink.records) != 30 {
		t.Fatal("lost middleware calls or logs")
	}
	seen := make(map[string]bool)
	for _, record := range acc.sink.records {
		id := record.fields["biz"].(map[string]any)["id"].(string)
		if seen[id] {
			t.Fatal("request state leaked")
		}
		seen[id] = true
	}
}
