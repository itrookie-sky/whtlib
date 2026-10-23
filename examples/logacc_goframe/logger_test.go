package main

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/gogf/gf/v2/os/glog"
	"github.com/itrookie-sky/whtlib/adapter"
	"github.com/itrookie-sky/whtlib/extension/logx"
	"github.com/itrookie-sky/whtlib/toolkit/tkhttp"
)

type lockedBuffer struct {
	mu sync.Mutex
	bytes.Buffer
}

func (b *lockedBuffer) Write(data []byte) (n int, err error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.Buffer.Write(data)
}

func newBackend() (native *glog.Logger, backend adapter.Logger, buffer *lockedBuffer) {
	buffer = new(lockedBuffer)
	native = glog.NewWithWriter(buffer)
	native.SetStdoutPrint(false)
	native.SetWriterColorEnable(false)
	native.SetFlags(glog.F_FILE_SHORT)
	native.SetStack(false)
	native.SetLevel(glog.LEVEL_ALL)
	return native, NewGoFrameLogger(native), buffer
}

func TestGoFrameLogAccAndCaller(t *testing.T) {
	native, backend, buffer := newBackend()
	type contextKey string
	key := contextKey("requestId")
	native.SetCtxKeys(key)
	ctx := context.WithValue(t.Context(), key, "request-001")
	acc := logx.NewLogAcc(logx.WithLogger(backend))
	_, file, line, _ := runtime.Caller(0)
	acc.SetValue("orderId", "demo").Info(ctx, "done", "count", 2)
	output := buffer.String()
	if !strings.Contains(output, filepath.Base(file)+":"+strconv.Itoa(line+1)) ||
		!strings.Contains(output, `done {"count":2,"orderId":"demo"}`) || !strings.Contains(output, "request-001") {
		t.Fatal(output)
	}
	buffer.Reset()
	shared := logx.NewLogAccAdapter(logx.WithLogger(backend))
	_, _, line, _ = runtime.Caller(0)
	shared.Info(ctx, "shared")
	if !strings.Contains(buffer.String(), filepath.Base(file)+":"+strconv.Itoa(line+1)) {
		t.Fatal(buffer.String())
	}
	buffer.Reset()
	print := func() { backend.Skip(1).Info(ctx, "wrapped") }
	_, _, line, _ = runtime.Caller(0)
	print()
	if !strings.Contains(buffer.String(), filepath.Base(file)+":"+strconv.Itoa(line+1)) {
		t.Fatal(buffer.String())
	}
	if native.GetConfig().StSkip != 0 || backend.(*goFrameLogger).skip != 0 {
		t.Fatal("borrowed backend was mutated")
	}
}

type countedJSON struct{ calls *int }

func (v countedJSON) MarshalJSON() (data []byte, err error) {
	*v.calls++
	return []byte(`"value"`), nil
}

func TestGoFrameLevelsFormattingAndFiltering(t *testing.T) {
	native, backend, buffer := newBackend()
	for _, print := range []func(context.Context, string, ...any){backend.Debug, backend.Info, backend.Warn, backend.Error} {
		print(t.Context(), "item", 1, "ready")
	}
	for _, print := range []func(context.Context, string, ...any){backend.Debugf, backend.Infof, backend.Warnf, backend.Errorf} {
		print(t.Context(), "item %d %s", 1, "ready")
	}
	if strings.Count(buffer.String(), "item 1 ready\n") != 8 {
		t.Fatal(buffer.String())
	}
	buffer.Reset()
	native.SetLevel(glog.LEVEL_INFO)
	calls := 0
	acc := logx.NewLogAcc(logx.WithLogger(backend)).SetValue("data", countedJSON{&calls})
	acc.Debug(t.Context(), "filtered")
	if calls != 0 || buffer.Len() != 0 {
		t.Fatal("filtered fields were formatted")
	}
	acc.Info(t.Context(), "visible")
	if calls != 1 || !strings.Contains(buffer.String(), `visible {"data":"value"}`) {
		t.Fatal(buffer.String())
	}
}

func TestGoFrameConcurrentBorrowing(t *testing.T) {
	native, backend, buffer := newBackend()
	var group sync.WaitGroup
	for worker := range 4 {
		group.Go(func() {
			for range 10 {
				acc := logx.NewLogAcc(logx.WithLogger(backend))
				acc.SetValue("worker", worker).Info(t.Context(), "done")
			}
		})
	}
	group.Wait()
	if strings.Count(buffer.String(), "\n") != 40 || native.GetConfig().StSkip != 0 {
		t.Fatal(buffer.String())
	}
}

func TestGoFrameHTTPFactory(t *testing.T) {
	_, backend, buffer := newBackend()
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(server.Close)
	client, err := newHTTPClient(server.URL, backend)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(client.CloseIdleConnections)
	_, file, line, _ := runtime.Caller(0)
	_, err = client.Get(t.Context(), "/first", tkhttp.RequestConfig{LogFields: map[string]any{"orderId": "first"}})
	if err != nil {
		t.Fatal(err)
	}
	first := buffer.String()
	if strings.Count(first, "\n") != 1 || !strings.Contains(first, filepath.Base(file)+":"+strconv.Itoa(line+1)) ||
		!strings.Contains(first, `"biz":{"orderId":"first"}`) || !strings.Contains(first, `"statusCode":204`) {
		t.Fatal(first)
	}
	buffer.Reset()
	_, err = client.Get(t.Context(), "/second")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(buffer.String(), "\n") != 1 || strings.Contains(buffer.String(), "orderId") {
		t.Fatal("HTTP calls shared fields: ", buffer.String())
	}
}
