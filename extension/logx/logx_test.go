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
type testCtxKey string

func newTestLogger(t *testing.T, config Config) (logger *Logger) {
	t.Helper()
	config.WriteFile = true
	if config.File.Path == "" {
		config.File.Path = t.TempDir()
	}
	logger, err := New(config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = logger.Close() })
	return
}

// lockedTestWriter 仅用于测试注入编码与写入故障，不属于公开配置。
type lockedTestWriter struct {
	mu     sync.Mutex
	writer io.Writer
}

func (w *lockedTestWriter) Write(p []byte) (n int, err error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.writer.Write(p)
}

func newBufferedLogger(t *testing.T, writer io.Writer, config Config) (logger *Logger) {
	logger = newTestLogger(t, config)
	logger.core.handler.state.writer = &lockedTestWriter{writer: writer}
	return
}

func testSkip(t *testing.T, logger *Logger, n int) (derived *Logger) {
	derived = logger.Skip(n)
	t.Cleanup(func() { _ = derived.Close() })
	return
}

func TestContextAndAttributes(t *testing.T) {
	var buffer bytes.Buffer
	logger := newBufferedLogger(t, &buffer, Config{TraceKey: testKey(1), ContextFields: []ContextField{
		{Name: "userId", Key: testKey(2)}, {Name: "zero", Key: testKey(3)},
		{Name: "false", Key: testKey(4)}, {Name: "empty", Key: testKey(5)}, {Name: "nil", Key: testKey(6)},
	}})
	ctx := context.WithValue(t.Context(), testKey(1), "a\"\nb")
	ctx = context.WithValue(ctx, testKey(2), 123)
	ctx = context.WithValue(ctx, testKey(3), 0)
	ctx = context.WithValue(ctx, testKey(4), false)
	ctx = context.WithValue(ctx, testKey(5), "")
	ctx = context.WithValue(ctx, testKey(6), (*int)(nil))
	NewLogAcc(WithLogger(logger)).Info(ctx, "created\norder", "orderId", "o1")
	line := buffer.String()
	if !strings.Contains(line, ` [INFO] {a\"\nb} {"userId":123,"zero":0,"false":false} `) || !strings.HasSuffix(line, "created\\norder {\"orderId\":\"o1\"}\n") || strings.Count(line, "\n") != 1 {
		t.Fatalf("unexpected record: %q", line)
	}
}

func TestCallerAndSkip(t *testing.T) {
	var buffer bytes.Buffer
	logger := newBufferedLogger(t, &buffer, Config{})
	_, file, line, _ := runtime.Caller(0)
	logger.Info(t.Context(), "direct")
	expected := fmt.Sprintf("%s:%d direct", filepath.Base(file), line+1)
	if !strings.Contains(buffer.String(), expected) {
		t.Fatalf("want %q, got %q", expected, buffer.String())
	}
	buffer.Reset()
	_, _, line, _ = runtime.Caller(0)
	wrappedLog(t, logger)
	expected = fmt.Sprintf("%s:%d wrapped", filepath.Base(file), line+1)
	if !strings.Contains(buffer.String(), expected) {
		t.Fatalf("want %q, got %q", expected, buffer.String())
	}
	buffer.Reset()
	_, _, line, _ = runtime.Caller(0)
	logger.Infof(t.Context(), "number %d", 3)
	if !strings.Contains(buffer.String(), fmt.Sprintf("%s:%d number 3", filepath.Base(file), line+1)) {
		t.Fatalf("formatted caller: %s", buffer.String())
	}
	buffer.Reset()
	testSkip(t, logger, int(^uint(0)>>1)).Info(t.Context(), "no caller")
	if !strings.HasSuffix(buffer.String(), "[INFO] {} no caller\n") || logger.skip != 0 {
		t.Fatalf("skip overflow boundary: %q", buffer.String())
	}
}

func wrappedLog(t *testing.T, logger *Logger) {
	t.Helper()
	testSkip(t, logger, 1).Info(t.Context(), "wrapped")
}

type explosiveStringer struct{}

func (explosiveStringer) String() (result string) { panic("must not format") }

func TestFilteringCancellationAndClose(t *testing.T) {
	var buffer bytes.Buffer
	var level slog.LevelVar
	logger := newBufferedLogger(t, &buffer, Config{Level: &level})
	logger.Debugf(t.Context(), "%s", explosiveStringer{})
	if buffer.Len() != 0 {
		t.Fatal("filtered record was written")
	}
	logger.SetConfig(Config{Level: slog.LevelDebug})
	logger.core.handler.state.writer = &lockedTestWriter{writer: &buffer}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	logger.Debug(ctx, "cancelled")
	if !strings.Contains(buffer.String(), "cancelled") {
		t.Fatal("cancelled context discarded")
	}
	derived := logger.Skip(1)
	defer derived.Close()
	_ = logger.Close()
	derived.Info(t.Context(), "independent")
	_ = derived.Close()
	before := buffer.String()
	diagnostic := captureStderr(t, func() { derived.Info(t.Context(), "closed") })
	if !strings.Contains(diagnostic, os.ErrClosed.Error()) || !strings.Contains(diagnostic, "goroutine ") {
		t.Fatalf("closed logger diagnostic missing: %s", diagnostic)
	}
	if buffer.String() != before {
		t.Fatal("derived logger wrote after close")
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
	logger := newBufferedLogger(t, failingWriter{err: failure}, Config{})
	ctx := context.WithValue(t.Context(), "traceId", make(chan int))
	diagnostic := captureStderr(t, func() { logger.Info(ctx, "failed") })
	for _, expected := range []string{"write failure", "traceId", "goroutine ", "(*Logger).output", "TestErrorsAndOwnership"} {
		if !strings.Contains(diagnostic, expected) {
			t.Fatalf("missing %q in diagnostic: %s", expected, diagnostic)
		}
	}
	if strings.Count(diagnostic, "\ngoroutine ") != 1 {
		t.Fatalf("expected one stack for combined errors: %s", diagnostic)
	}
	short := newBufferedLogger(t, failingWriter{}, Config{})
	diagnostic = captureStderr(t, func() { short.Info(t.Context(), "short") })
	if !strings.Contains(diagnostic, io.ErrShortWrite.Error()) || !strings.Contains(diagnostic, "goroutine ") {
		t.Fatalf("short write diagnostic missing: %s", diagnostic)
	}
	var buffer closingBuffer
	owned := newBufferedLogger(t, &buffer, Config{})
	_ = owned.Close()
	if buffer.closed {
		t.Fatal("external writer closed")
	}
}

func TestGroups(t *testing.T) {
	var buffer bytes.Buffer
	logger := newBufferedLogger(t, &buffer, Config{})
	h := logger.core.handler.WithGroup("request").WithAttrs([]slog.Attr{slog.Int("a", 1)}).WithGroup("child")
	h = h.WithAttrs([]slog.Attr{slog.Int("b", 2)})
	record := slog.NewRecord(time.Time{}, slog.LevelInfo, "groups", 0)
	record.AddAttrs(slog.Int("c", 3), slog.Group(""), slog.Attr{})
	err := h.Handle(t.Context(), record)
	if err != nil || buffer.String() != "[INFO] {} groups {\"request\":{\"a\":1,\"child\":{\"b\":2,\"c\":3}}}\n" {
		t.Fatalf("group output: %q, %v", buffer.String(), err)
	}
}

func TestInvalidConfigAndPanic(t *testing.T) {
	var buffer bytes.Buffer
	cases := []Config{
		{Skip: -1}, {TraceKey: []string{"x"}},
		{ContextFields: []ContextField{{Name: "traceId", Key: "x"}}},
		{ContextFields: []ContextField{{Name: "user", Key: nil}}},
	}
	for _, config := range cases {
		_, err := New(config)
		if err == nil {
			t.Fatal("invalid config accepted")
		}
	}
	logger := newBufferedLogger(t, &buffer, Config{})
	for name, call := range map[string]func(){
		"nil context":   func() { logger.Info(nil, "invalid") },
		"negative skip": func() { logger.Skip(-1) },
		"overflow skip": func() { testSkip(t, logger, int(^uint(0)>>1)).Skip(1) },
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
	logger := newBufferedLogger(t, &buffer, Config{})
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			for range 50 {
				testSkip(t, logger, 0).Info(t.Context(), "concurrent")
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
	logger := newBufferedLogger(t, &buffer, Config{})
	diagnostic := captureStderr(t, func() {
		NewLogAcc(WithLogger(logger)).Info(t.Context(), "encoding", "group", slog.GroupValue(slog.Any("bad", make(chan int)), slog.Int("good", 1)))
	})
	if !strings.Contains(diagnostic, "encode") {
		t.Fatalf("encoding diagnostic missing: %s", diagnostic)
	}
	if !strings.Contains(buffer.String(), `{"group":{"bad":"[json encode error]","good":1}}`) {
		t.Fatalf("encoding discarded siblings: %q", buffer.String())
	}
}

// captureStderr 仅用于串行测试，避免真实堆栈诊断污染测试输出。
func captureStderr(t *testing.T, call func()) (output string) {
	t.Helper()
	file, err := os.CreateTemp(t.TempDir(), "stderr-*")
	if err != nil {
		t.Fatal(err)
	}
	previous := os.Stderr
	os.Stderr = file
	defer func() {
		os.Stderr = previous
		_ = file.Close()
	}()
	call()
	_, err = file.Seek(0, io.SeekStart)
	if err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(file)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestSuccessfulAndFilteredOutputHasNoDiagnostic(t *testing.T) {
	var buffer bytes.Buffer
	logger := newBufferedLogger(t, &buffer, Config{})
	diagnostic := captureStderr(t, func() {
		logger.Info(t.Context(), "success")
		logger.Debugf(t.Context(), "%s", explosiveStringer{})
	})
	if diagnostic != "" || !strings.Contains(buffer.String(), "success") {
		t.Fatalf("unexpected diagnostic %q or output %q", diagnostic, buffer.String())
	}
}

func TestTimestampLocalString(t *testing.T) {
	previous := time.Local
	time.Local = time.FixedZone("CST", 8*60*60)
	defer func() { time.Local = previous }()

	for _, tc := range []struct {
		nanos     int
		timestamp string
	}{
		{nanos: 0, timestamp: "2026-10-13 14:30:00"},
		{nanos: 123000000, timestamp: "2026-10-13 14:30:00.123"},
		{nanos: 123456789, timestamp: "2026-10-13 14:30:00.123456789"},
	} {
		t.Run(tc.timestamp, func(t *testing.T) {
			var buffer bytes.Buffer
			logger := newBufferedLogger(t, &buffer, Config{})
			record := slog.NewRecord(time.Date(2026, 10, 13, 14, 30, 0, tc.nanos, time.Local), slog.LevelInfo, "timestamp", 0)
			err := logger.core.handler.Handle(t.Context(), record)
			if err != nil {
				t.Fatal(err)
			}
			expected := tc.timestamp + " +0800 CST [INFO] {} timestamp\n"
			if buffer.String() != expected {
				t.Fatalf("want %q, got %q", expected, buffer.String())
			}
		})
	}
}

func TestLogInfo(t *testing.T) {
	traceKey := testCtxKey("traceId")
	ctx := context.WithValue(context.TODO(), traceKey, "321")
	logger, err := New(Config{
		WriteFile: true,
		Level:     slog.LevelDebug,
		TraceKey:  traceKey,
	})
	if err != nil {
		t.Fatal(err)
	}
	logger.Debug(ctx, "hello world")
}
