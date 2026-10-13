package logx

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

type testKey int

func newTestLogger(t *testing.T, options Options) (logger *Logger) {
	t.Helper()
	logger, err := New(options)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = logger.Close() })
	return
}

func TestContextAndAttributes(t *testing.T) {
	var buffer bytes.Buffer
	logger := newTestLogger(t, Options{Writer: &buffer, TraceKey: testKey(1), ContextFields: []ContextField{
		{Name: "userId", Key: testKey(2)}, {Name: "zero", Key: testKey(3)},
		{Name: "false", Key: testKey(4)}, {Name: "empty", Key: testKey(5)}, {Name: "nil", Key: testKey(6)},
	}})
	ctx := context.WithValue(t.Context(), testKey(1), "a\"\nb")
	ctx = context.WithValue(ctx, testKey(2), 123)
	ctx = context.WithValue(ctx, testKey(3), 0)
	ctx = context.WithValue(ctx, testKey(4), false)
	ctx = context.WithValue(ctx, testKey(5), "")
	ctx = context.WithValue(ctx, testKey(6), (*int)(nil))
	err := logger.Info(ctx, "created\norder", "orderId", "o1")
	if err != nil {
		t.Fatal(err)
	}
	line := buffer.String()
	if !strings.HasPrefix(line[20:], `{"traceId":"a\"\nb","userId":123,"zero":0,"false":false} `) || !strings.HasSuffix(line, "INFO created\\norder {\"orderId\":\"o1\"}\n") || strings.Count(line, "\n") != 1 {
		t.Fatalf("unexpected record: %q", line)
	}
}

func TestCallerAndSkip(t *testing.T) {
	var buffer bytes.Buffer
	logger := newTestLogger(t, Options{Writer: &buffer})
	_, file, line, _ := runtime.Caller(0)
	err := logger.Info(t.Context(), "direct")
	if err != nil {
		t.Fatal(err)
	}
	expected := fmt.Sprintf("%s:%d INFO direct", filepath.Base(file), line+1)
	if !strings.Contains(buffer.String(), expected) {
		t.Fatalf("want %q, got %q", expected, buffer.String())
	}
	buffer.Reset()
	_, _, line, _ = runtime.Caller(0)
	wrappedLog(t, logger)
	expected = fmt.Sprintf("%s:%d INFO wrapped", filepath.Base(file), line+1)
	if !strings.Contains(buffer.String(), expected) {
		t.Fatalf("want %q, got %q", expected, buffer.String())
	}
	buffer.Reset()
	_, _, line, _ = runtime.Caller(0)
	err = logger.Infof(t.Context(), "number %d", 3)
	if err != nil || !strings.Contains(buffer.String(), fmt.Sprintf("%s:%d INFO number 3", filepath.Base(file), line+1)) {
		t.Fatalf("formatted caller: %s, %v", buffer.String(), err)
	}
	buffer.Reset()
	err = logger.Skip(int(^uint(0)>>1)).Info(t.Context(), "no caller")
	if err != nil || !strings.HasSuffix(buffer.String(), "{} INFO no caller\n") || logger.skip != 0 {
		t.Fatalf("skip overflow boundary: %q, %v", buffer.String(), err)
	}
}

func wrappedLog(t *testing.T, logger *Logger) {
	t.Helper()
	err := logger.Skip(1).Info(t.Context(), "wrapped")
	if err != nil {
		t.Fatal(err)
	}
}

type explosiveStringer struct{}

func (explosiveStringer) String() (result string) { panic("must not format") }

func TestFilteringCancellationAndClose(t *testing.T) {
	var buffer bytes.Buffer
	var level slog.LevelVar
	logger := newTestLogger(t, Options{Writer: &buffer, Level: &level})
	err := logger.Debugf(t.Context(), "%s", explosiveStringer{})
	if err != nil || buffer.Len() != 0 {
		t.Fatal("filtered record was written")
	}
	level.Set(slog.LevelDebug)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	err = logger.Debug(ctx, "cancelled")
	if err != nil || !strings.Contains(buffer.String(), "DEBUG cancelled") {
		t.Fatal("cancelled context discarded")
	}
	derived := logger.Skip(1)
	_ = logger.Close()
	if !errors.Is(derived.Info(t.Context(), "closed"), os.ErrClosed) {
		t.Fatal("derived logger did not close")
	}
}

type failingWriter struct{ err error }

func (w failingWriter) Write(p []byte) (n int, err error) { return 0, w.err }

type closingBuffer struct {
	bytes.Buffer
	closed bool
}

func (b *closingBuffer) Close() (err error) { b.closed = true; return }

func TestErrorsAndOwnership(t *testing.T) {
	failure := errors.New("write failure")
	logger := newTestLogger(t, Options{Writer: failingWriter{err: failure}})
	ctx := context.WithValue(t.Context(), "traceId", make(chan int))
	err := logger.Info(ctx, "failed")
	if !errors.Is(err, failure) || !strings.Contains(err.Error(), "traceId") {
		t.Fatalf("missing combined error: %v", err)
	}
	short := newTestLogger(t, Options{Writer: failingWriter{}})
	if !errors.Is(short.Info(t.Context(), "short"), io.ErrShortWrite) {
		t.Fatal("short write missing")
	}
	var buffer closingBuffer
	owned := newTestLogger(t, Options{Writer: &buffer})
	_ = owned.Close()
	if buffer.closed {
		t.Fatal("external writer closed")
	}
}

func TestGroups(t *testing.T) {
	var buffer bytes.Buffer
	logger := newTestLogger(t, Options{Writer: &buffer})
	h := logger.handler.WithGroup("request").WithAttrs([]slog.Attr{slog.Int("a", 1)}).WithGroup("child")
	h = h.WithAttrs([]slog.Attr{slog.Int("b", 2)})
	record := slog.NewRecord(time.Time{}, slog.LevelInfo, "groups", 0)
	record.AddAttrs(slog.Int("c", 3), slog.Group(""), slog.Attr{})
	err := h.Handle(t.Context(), record)
	if err != nil || buffer.String() != "{} INFO groups {\"request\":{\"a\":1,\"child\":{\"b\":2,\"c\":3}}}\n" {
		t.Fatalf("group output: %q, %v", buffer.String(), err)
	}
}

func TestInvalidOptionsAndPanic(t *testing.T) {
	var buffer bytes.Buffer
	cases := []Options{
		{Writer: &buffer, Skip: -1}, {Writer: &buffer, TraceKey: []string{"x"}},
		{Writer: &buffer, ContextFields: []ContextField{{Name: "traceId", Key: "x"}}},
		{Writer: &buffer, ContextFields: []ContextField{{Name: "user", Key: nil}}},
	}
	for _, options := range cases {
		_, err := New(options)
		if err == nil {
			t.Fatal("invalid options accepted")
		}
	}
	logger := newTestLogger(t, Options{Writer: &buffer})
	for name, call := range map[string]func(){
		"nil context":   func() { _ = logger.Info(nil, "invalid") },
		"negative skip": func() { logger.Skip(-1) },
		"overflow skip": func() { logger.Skip(int(^uint(0) >> 1)).Skip(1) },
	} {
		t.Run(name, func(t *testing.T) {
			defer func() {
				if recover() == nil {
					t.Error("expected panic")
				}
			}()
			call()
		})
	}
}

func TestConcurrentLogger(t *testing.T) {
	var buffer bytes.Buffer
	logger := newTestLogger(t, Options{Writer: &buffer})
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			for range 50 {
				err := logger.Skip(0).Info(t.Context(), "concurrent")
				if err != nil {
					t.Error(err)
				}
			}
		})
	}
	wg.Wait()
	if strings.Count(buffer.String(), "\n") != 400 {
		t.Fatal("records lost or interleaved")
	}
}

func TestGroupEncodingErrorPreservesSiblings(t *testing.T) {
	var buffer bytes.Buffer
	logger := newTestLogger(t, Options{Writer: &buffer})
	err := logger.Info(t.Context(), "encoding", slog.Group("group", slog.Any("bad", make(chan int)), slog.Int("good", 1)))
	if err == nil || !strings.Contains(buffer.String(), `{"group":{"bad":"[json encode error]","good":1}}`) {
		t.Fatalf("encoding discarded siblings: %q, %v", buffer.String(), err)
	}
}
