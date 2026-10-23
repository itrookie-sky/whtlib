package tkhttp

import (
	"context"
	"errors"
	"net/http"
	"sync/atomic"
	"testing"
	"time"
)

type waitingJSON struct{ ctx context.Context }

func (value waitingJSON) MarshalJSON() (data []byte, err error) {
	<-value.ctx.Done()
	return []byte(`{}`), nil
}

type waitingResult struct{ ctx context.Context }

func (value *waitingResult) UnmarshalJSON(data []byte) (err error) {
	<-value.ctx.Done()
	return nil
}

func TestTimeoutOverridesAndNativeTimeout(t *testing.T) {
	cases := []struct {
		name    string
		client  time.Duration
		request time.Duration
		native  time.Duration
	}{
		{name: "inherited", client: 5 * time.Millisecond},
		{name: "request override", client: time.Hour, request: 5 * time.Millisecond},
		{name: "native timeout", client: -1, native: 5 * time.Millisecond},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var calls atomic.Int32
			transport := roundTripFunc(func(request *http.Request) (response *http.Response, err error) {
				calls.Add(1)
				<-request.Context().Done()
				return nil, request.Context().Err()
			})
			config := quietConfig()
			config.Timeout = tc.client
			client := mustClient(t, config, WithHTTPClient(&http.Client{Transport: transport, Timeout: tc.native}))
			response, err := client.Get(t.Context(), "resource", RequestConfig{Timeout: tc.request})
			if response != nil || !errors.Is(err, context.DeadlineExceeded) || calls.Load() != 1 {
				t.Fatalf("response=%v err=%v calls=%d", response, err, calls.Load())
			}
		})
	}
	config := quietConfig()
	config.Timeout = time.Nanosecond
	transport := roundTripFunc(func(request *http.Request) (response *http.Response, err error) {
		if _, deadline := request.Context().Deadline(); deadline {
			t.Error("disabled component timeout created a deadline")
		}
		return nativeResponse(200, "ok"), nil
	})
	client := mustClient(t, config, WithHTTPClient(&http.Client{Transport: transport}))
	_, err := client.Get(t.Context(), "resource", RequestConfig{Timeout: -1})
	if err != nil {
		t.Fatal(err)
	}
	if client.config.Timeout != time.Nanosecond {
		t.Fatal("request override changed client timeout")
	}
}

func TestCancellationAndDeadlineAcrossPhases(t *testing.T) {
	t.Run("already canceled", func(t *testing.T) {
		var calls atomic.Int32
		transport := roundTripFunc(func(request *http.Request) (response *http.Response, err error) {
			calls.Add(1)
			return nativeResponse(200, ""), nil
		})
		client := mustClient(t, quietConfig(), WithHTTPClient(&http.Client{Transport: transport}))
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		_, err := client.Get(ctx, "resource")
		if !errors.Is(err, context.Canceled) || calls.Load() != 0 {
			t.Fatalf("err=%v calls=%d", err, calls.Load())
		}
	})
	for _, phase := range []string{"prepare", "hook", "read", "decode"} {
		t.Run(phase, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Millisecond)
			defer cancel()
			var calls atomic.Int32
			body := &trackedBody{reader: readFunc(func(data []byte) (n int, err error) { <-ctx.Done(); return 0, ctx.Err() })}
			transport := roundTripFunc(func(request *http.Request) (response *http.Response, err error) {
				calls.Add(1)
				response = nativeResponse(200, `{}`)
				if phase == "read" {
					response.Body = body
				}
				return
			})
			opts := []Option{WithHTTPClient(&http.Client{Transport: transport})}
			if phase == "hook" {
				opts = append(opts, WithRequestHook(func(ctx context.Context, info RequestInfo) (headers http.Header, err error) {
					<-ctx.Done()
					return nil, nil
				}))
			}
			client := mustClient(t, quietConfig(), opts...)
			var requestConfig RequestConfig
			if phase == "prepare" {
				requestConfig.JSON = waitingJSON{ctx: ctx}
			}
			if phase == "decode" {
				requestConfig.Result = &waitingResult{ctx: ctx}
			}
			response, err := client.Post(ctx, "resource", requestConfig)
			failure, ok := errors.AsType[*RequestError](err)
			if !ok || failure.Phase != phase || !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("phase=%s response=%v err=%v", phase, response, err)
			}
			if phase == "prepare" || phase == "hook" {
				if calls.Load() != 0 || response != nil {
					t.Fatal("timed-out preparation sent network request")
				}
				return
			}
			if calls.Load() != 1 || response == nil || phase == "read" && body.closed.Load() != 1 {
				t.Fatal("network count, response preservation or body cleanup is invalid")
			}
		})
	}
}
