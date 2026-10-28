package tkhttp

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/itrookie-sky/whtlib/adapter"
	"github.com/itrookie-sky/whtlib/extension/logx"
)

func TestLogFactoryIsolationAndRedaction(t *testing.T) {
	acc := newRecordingAcc()
	transport := roundTripFunc(func(request *http.Request) (response *http.Response, err error) {
		return nativeResponse(200, "private-response"), nil
	})
	config := quietConfig()
	config.DisableLog = false
	client := mustClient(t, config, WithLogAcc(func() (instance adapter.LogAcc) {
		instance = acc.newAcc()
		instance.SetValue("component", "original")
		instance.SetValue("http", "stale")
		instance.SetValue("biz", "stale")
		return
	}), WithHTTPClient(&http.Client{Transport: transport}))
	if acc.sink.creates.Load() != 0 {
		t.Fatal("New must not call the log factory")
	}
	before := time.Now()
	nested := map[string]any{"items": []string{"original"}, "bytes": []byte("original")}
	fields := map[string]any{"nested": nested, "http": "business"}
	type traceKey struct{}
	ctx := context.WithValue(t.Context(), traceKey{}, "trace-123")
	_, err := client.Post(ctx, "resource?secret=query", RequestConfig{
		Header: http.Header{"Authorization": {"private-auth"}}, Body: []byte("private-body"), LogFields: fields,
	})
	if err != nil {
		t.Fatal(err)
	}
	nested["items"].([]string)[0] = "changed"
	nested["bytes"].([]byte)[0] = 'x'
	_, err = client.Get(ctx, "resource")
	if err != nil {
		t.Fatal(err)
	}
	records := acc.sink.records
	if len(records) != 2 || acc.sink.creates.Load() != 2 || acc.sink.starts.Load() != 2 {
		t.Fatalf("records=%d creates=%d starts=%d", len(records), acc.sink.creates.Load(), acc.sink.starts.Load())
	}
	for _, record := range records {
		if record.start.Before(before) || record.level != "info" || record.ctx.Value(traceKey{}) != "trace-123" || record.fields["component"] != "original" || record.skip != 3 {
			t.Fatalf("invalid independent log: %+v", record)
		}
		payload, marshalErr := json.Marshal(record.fields)
		if marshalErr != nil || strings.Contains(string(payload), "private-") || strings.Contains(string(payload), "secret") {
			t.Fatalf("metadata leaked input: %s %v", payload, marshalErr)
		}
		group := record.fields["http"].(map[string]any)
		if len(group) != 8 || group["url"] != "https://api.example.com/v1/resource" || group["statusCode"] != 200 || group["phase"] != "complete" {
			t.Fatalf("invalid HTTP metadata: %v", group)
		}
	}
	firstBiz := records[0].fields["biz"].(map[string]any)
	firstNested := firstBiz["nested"].(map[string]any)
	if firstBiz["http"] != "business" || firstNested["items"].([]string)[0] != "original" || string(firstNested["bytes"].([]byte)) != "original" || len(records[1].fields["biz"].(map[string]any)) != 0 {
		t.Fatal("business field snapshots were shared or carried into next request")
	}
}

func TestLogsForErrorsAndCancellation(t *testing.T) {
	cases := []struct {
		name   string
		status int
		cancel bool
		badCtx bool
		level  string
		kind   string
	}{
		{name: "redirect", status: 302, level: "warn", kind: "httpStatus"},
		{name: "client status", status: 400, level: "warn", kind: "httpStatus"},
		{name: "server status", status: 503, level: "error", kind: "httpStatus"},
		{name: "cancel", cancel: true, level: "warn", kind: "canceled"},
		{name: "nil context", badCtx: true, level: "error", kind: "invalidRequest"},
		{name: "transport", level: "error", kind: "transport"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			acc := newRecordingAcc()
			var calls atomic.Int32
			transport := roundTripFunc(func(request *http.Request) (response *http.Response, err error) {
				calls.Add(1)
				if tc.status > 0 {
					return nativeResponse(tc.status, "private-body"), nil
				}
				return nil, errors.New("private transport cause")
			})
			config := quietConfig()
			config.DisableLog = false
			client := mustClient(t, config, WithLogAcc(acc.newAcc), WithHTTPClient(&http.Client{Transport: transport}))
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			if tc.cancel {
				cancel()
			}
			var supplied context.Context = ctx
			if tc.badCtx {
				supplied = nil
			}
			_, err := client.Get(supplied, "resource?secret=query")
			if err == nil || len(acc.sink.records) != 1 {
				t.Fatalf("err=%v records=%d", err, len(acc.sink.records))
			}
			record := acc.sink.records[0]
			if record.level != tc.level || record.ctx == nil || record.fields["http"].(map[string]any)["errorKind"] != tc.kind {
				t.Fatalf("record=%+v", record)
			}
			if !tc.badCtx && record.ctx != ctx {
				t.Fatal("log context was replaced")
			}
			if (tc.cancel || tc.badCtx) && calls.Load() != 0 {
				t.Fatal("invalid/canceled context sent a request")
			}
			encoded, err := json.Marshal(record.fields)
			if err != nil || strings.Contains(string(encoded), "private") || strings.Contains(string(encoded), "secret") {
				t.Fatalf("unsafe log fields: %s %v", encoded, err)
			}
		})
	}
}

func TestInvalidFactoryResultAndDisabledLogging(t *testing.T) {
	for _, typedNil := range []bool{false, true} {
		acc := newRecordingAcc()
		acc.sink.failAt = 1
		acc.sink.typedNil = typedNil
		var calls atomic.Int32
		transport := roundTripFunc(func(request *http.Request) (response *http.Response, err error) {
			calls.Add(1)
			return nativeResponse(200, ""), nil
		})
		config := quietConfig()
		config.DisableLog = false
		client := mustClient(t, config, WithLogAcc(acc.newAcc), WithHTTPClient(&http.Client{Transport: transport}))
		if acc.sink.creates.Load() != 0 {
			t.Fatal("New must only validate the factory, not create an instance")
		}
		_, err := client.Get(t.Context(), "resource")
		if !errors.Is(err, ErrLogAcc) || calls.Load() != 0 || len(acc.sink.records) != 0 {
			t.Fatalf("invalid factory result executed request: %v", err)
		}
	}
	acc := newRecordingAcc()
	acc.sink.failAt = 1
	transport := roundTripFunc(func(request *http.Request) (response *http.Response, err error) { return nativeResponse(200, ""), nil })
	client := mustClient(t, quietConfig(), WithLogAcc(acc.newAcc), WithHTTPClient(&http.Client{Transport: transport}))
	_, err := client.Get(t.Context(), "resource")
	if err != nil || acc.sink.creates.Load() != 0 || acc.sink.starts.Load() != 0 || len(acc.sink.records) != 0 {
		t.Fatal("disabled logs touched injected accumulator")
	}
}

func TestConcurrentRequestsKeepLogFieldsIndependent(t *testing.T) {
	acc := newRecordingAcc()
	transport := roundTripFunc(func(request *http.Request) (response *http.Response, err error) {
		return nativeResponse(200, "ok"), nil
	})
	config := quietConfig()
	config.DisableLog = false
	client := mustClient(t, config, WithLogAcc(acc.newAcc), WithHTTPClient(&http.Client{Transport: transport}))
	var wg sync.WaitGroup
	for id := range 32 {
		wg.Go(func() {
			_, err := client.Get(t.Context(), "resource", RequestConfig{LogFields: map[string]any{"id": id}})
			if err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	ids := make(map[int]bool)
	for _, record := range acc.sink.records {
		id := record.fields["biz"].(map[string]any)["id"].(int)
		if ids[id] || record.skip != 3 {
			t.Fatalf("shared request fields or accumulated Skip: %+v", record)
		}
		ids[id] = true
	}
	if len(ids) != 32 || acc.sink.creates.Load() != 32 {
		t.Fatal("lost requests or unexpected factory call count")
	}
}

func TestLogxIntegrationCallerAndTrace(t *testing.T) {
	type traceKey struct{}
	dir := t.TempDir()
	logger, err := logx.New(logx.Config{WriteFile: true, TraceKey: traceKey{}, File: logx.FileConfig{Path: dir, File: "http.log"}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = logger.Close() })
	transport := roundTripFunc(func(request *http.Request) (response *http.Response, err error) {
		return nativeResponse(200, "ok"), nil
	})
	config := quietConfig()
	config.DisableLog = false
	client := mustClient(t, config, WithLogAcc(func() (acc adapter.LogAcc) {
		return logx.NewLogAcc(logx.WithLogger(logx.NewLoggerAdapter(logger)))
	}), WithHTTPClient(&http.Client{Transport: transport}), WithMiddleware(func(next Handler) (handler Handler) { return next }))
	ctx := context.WithValue(t.Context(), traceKey{}, "trace-http")
	_, file, doLine, _ := runtime.Caller(0)
	_, err = client.Do(ctx, "GET", "resource")
	if err != nil {
		t.Fatal(err)
	}
	_, _, getLine, _ := runtime.Caller(0)
	_, err = client.Get(ctx, "resource")
	if err != nil {
		t.Fatal(err)
	}
	_, _, postLine, _ := runtime.Caller(0)
	_, err = client.Post(ctx, "resource")
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "http.log"))
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range []int{doLine + 1, getLine + 1, postLine + 1} {
		if !strings.Contains(string(data), filepath.Base(file)+":"+strconv.Itoa(line)+" [") {
			t.Fatalf("caller line %d missing: %s", line, data)
		}
	}
	if strings.Count(string(data), "{trace-http}") != 3 || strings.Count(string(data), "HTTP 请求完成") != 3 {
		t.Fatalf("missing trace or duplicate logs: %s", data)
	}
}

func TestCyclicBusinessFieldsDoNotRecurseDuringCopy(t *testing.T) {
	fields := map[string]any{}
	fields["self"] = fields
	clone := cloneLogFields(fields)
	clone["copyOnly"] = true
	if len(fields) != 1 || clone["self"].(map[string]any)["copyOnly"] != true {
		t.Fatal("cycle was not copied independently")
	}
}
