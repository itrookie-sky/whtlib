package logx

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/itrookie-sky/whtlib/adapter"
)

func TestLogAccOverwriteAndSnapshot(t *testing.T) {
	var buffer bytes.Buffer
	logger := newBufferedLogger(t, &buffer, Config{})
	acc := NewLogAcc(WithLogger(NewLoggerAdapter(logger)))
	acc.SetValue("one", 1)
	acc.Update(map[string]any{"one": 2, "two": true})
	source := map[string]any{"three": "before"}
	acc.Update(source)
	source["three"] = "after"
	acc.Update(map[string]any{"one": 3, "nil": nil})
	acc.Info(t.Context(), "first")
	acc.Info(t.Context(), "second")
	for _, line := range strings.Split(strings.TrimSpace(buffer.String()), "\n") {
		if !strings.Contains(line, `{"nil":null,"one":3,"three":"before","two":true}`) {
			t.Fatal(line)
		}
	}
	fields, _, _, _ := acc.snapshot()
	fields["one"] = "outside"
	acc.Info(t.Context(), "isolated")
	if strings.Contains(buffer.String(), "outside") {
		t.Fatal("snapshot exposed internal map")
	}
}

// logAccHandle 模拟 lib 的函数级日志：先注册打印，再注册最终错误采集。
func logAccHandle(ctx context.Context, handle func() (string, error), ops ...LogAccOptionFunc) (resRaw string, err error) {
	acc := NewLogAcc(ops...)
	acc.SetValue("resRaw", &resRaw)
	defer acc.Info(ctx, "HandleDevice")
	defer acc.SetErr(&err)
	acc.Update(map[string]any{"deviceCode": "device-001", "stage": "start"})
	acc.SetValue("stage", "handle")
	resRaw, err = handle()
	if err != nil {
		return
	}
	acc.Update(map[string]any{"stage": "done", "success": true})
	return
}

func TestLogAccDeferredLifecycle(t *testing.T) {
	failure := errors.New("handle failed")
	for _, injected := range []bool{false, true} {
		for _, fail := range []bool{false, true} {
			t.Run(fmt.Sprintf("injected=%v/error=%v", injected, fail), func(t *testing.T) {
				var globalBuffer, customBuffer bytes.Buffer
				previous := defaultLogger
				defaultLogger = newBufferedLogger(t, &globalBuffer, Config{})
				t.Cleanup(func() { defaultLogger = previous })
				ops := []LogAccOptionFunc{WithCost(time.Now())}
				output := &globalBuffer
				if injected {
					logger := newBufferedLogger(t, &customBuffer, Config{})
					ops = append(ops, WithLogger(NewLoggerAdapter(logger)))
					output = &customBuffer
				}
				// 在业务执行期间不应打印；两个 defer 都在命名返回值赋值后执行。
				result, err := logAccHandle(t.Context(), func() (response string, err error) {
					if globalBuffer.Len() != 0 || customBuffer.Len() != 0 {
						t.Fatal("logged before function exit")
					}
					if fail {
						return "partial response", failure
					}
					return "final response", nil
				}, ops...)
				if fail && !errors.Is(err, failure) || !fail && err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				line := output.String()
				if strings.Count(line, "\n") != 1 {
					t.Fatalf("expected one exit record: %q", line)
				}
				_, body, ok := strings.Cut(line, " [INFO] ")
				_, body, hasCost := strings.Cut(body, " [")
				if !ok || !hasCost {
					t.Fatal(line)
				}
				cost, data, ok := strings.Cut(body, "] HandleDevice ")
				if !ok {
					t.Fatal(line)
				}
				duration, parseErr := time.ParseDuration(cost)
				if parseErr != nil || duration < 0 {
					t.Fatalf("invalid elapsed time: %q", cost)
				}
				var fields map[string]any
				parseErr = json.Unmarshal([]byte(data), &fields)
				if parseErr != nil {
					t.Fatal(parseErr)
				}
				if fields["deviceCode"] != "device-001" || fields["resRaw"] != result || result == "" {
					t.Fatalf("lost final values: %#v", fields)
				}
				if fail {
					if fields["err"] != failure.Error() || fields["stage"] != "handle" {
						t.Fatalf("early return lost fields: %#v", fields)
					}
					if _, found := fields["success"]; found {
						t.Fatal("error path ran success update")
					}
				} else {
					if fields["stage"] != "done" || fields["success"] != true {
						t.Fatalf("updates lost: %#v", fields)
					}
					if _, found := fields["err"]; found {
						t.Fatal("nil error created err field")
					}
				}
				if injected && globalBuffer.Len() != 0 {
					t.Fatal("custom logger also used global logger")
				}
			})
		}
	}
}

func TestLogAccNilErrorPreservesFields(t *testing.T) {
	var buffer bytes.Buffer
	logger := newBufferedLogger(t, &buffer, Config{})
	acc := NewLogAcc(WithLogger(NewLoggerAdapter(logger)))
	err := errors.New("original error")
	acc.SetErr(&err)
	acc.SetErr(nil)
	var empty error
	acc.SetErr(&empty)
	acc.Info(t.Context(), "retained")
	if !strings.Contains(buffer.String(), `"err":"original error"`) {
		t.Fatal(buffer.String())
	}
}

func TestLogAccZeroValueUsesDefault(t *testing.T) {
	var buffer bytes.Buffer
	previous := defaultLogger
	defaultLogger = newBufferedLogger(t, &buffer, Config{})
	t.Cleanup(func() { defaultLogger = previous })
	var acc LogAcc
	acc.SetValue("zero", true)
	acc.Info(t.Context(), "zero value")
	if !strings.Contains(buffer.String(), `"zero":true`) {
		t.Fatal(buffer.String())
	}
}

func TestLogAccCallersAndCost(t *testing.T) {
	var buffer bytes.Buffer
	logger := newBufferedLogger(t, &buffer, Config{Level: slog.LevelDebug})
	acc := NewLogAcc(WithLogger(NewLoggerAdapter(logger)))
	_, file, line, _ := runtime.Caller(0)
	acc.Info(t.Context(), "direct")
	if !strings.Contains(buffer.String(), filepath.Base(file)+":"+strconv.Itoa(line+1)+" direct") {
		t.Fatal(buffer.String())
	}
	buffer.Reset()
	acc.Skip(1)
	_, _, line, _ = runtime.Caller(0)
	logAccWrapped(t.Context(), acc)
	if !strings.Contains(buffer.String(), filepath.Base(file)+":"+strconv.Itoa(line+1)+" wrapped") {
		t.Fatal(buffer.String())
	}
	buffer.Reset()
	cost := NewLogAcc(WithLogger(NewLoggerAdapter(logger)), WithCost(time.Now().Add(-time.Second)))
	cost.Info(t.Context(), "cost")
	if !strings.Contains(buffer.String(), " [INFO] ") || !strings.Contains(buffer.String(), "] cost") {
		t.Fatal(buffer.String())
	}
	// 累积器打印不得创建 Logger.Skip 派生文件引用。
	sharedFiles.mu.Lock()
	refs := len(logger.core.handler.state.lease.entry.leases)
	sharedFiles.mu.Unlock()
	if refs != 1 {
		t.Fatalf("leaked %d references", refs)
	}
}

func logAccWrapped(ctx context.Context, acc *LogAcc) { acc.Info(ctx, "wrapped") }

func TestLogAccConcurrentUpdatesAndPrint(t *testing.T) {
	var buffer bytes.Buffer
	logger := newBufferedLogger(t, &buffer, Config{})
	acc := NewLogAcc(WithLogger(NewLoggerAdapter(logger)))
	var wg sync.WaitGroup
	for i := range 4 {
		wg.Go(func() {
			for j := range 40 {
				acc.SetValue(fmt.Sprintf("worker%d", i), j)
				acc.Update(map[string]any{"left": j, "right": j})
				acc.Info(t.Context(), "concurrent")
			}
		})
	}
	wg.Wait()
	lines := strings.Split(strings.TrimSpace(buffer.String()), "\n")
	if len(lines) != 160 {
		t.Fatalf("missing records: %d", len(lines))
	}
	for _, line := range lines {
		_, data, ok := strings.Cut(line, " concurrent ")
		if !ok {
			t.Fatal(line)
		}
		var fields map[string]any
		err := json.Unmarshal([]byte(data), &fields)
		if err != nil {
			t.Fatal(err)
		}
		if fields["left"] != fields["right"] {
			t.Fatal("partial batch snapshot")
		}
	}
}

func TestLogAccArgumentsPreserveFields(t *testing.T) {
	acc := NewLogAcc()
	for _, args := range [][]any{{"one"}, {"one", 1, 2, 3}} {
		acc.Info(t.Context(), args...)
	}
	fields, _, _, _ := acc.snapshot()
	if len(fields) != 0 {
		t.Fatal("body arguments changed accumulated fields")
	}
}

// logAccRequest 对齐 libclient.DoRequest 的延迟 Debug、Skip 和返回值指针用法。
func logAccRequest(ctx context.Context, logger *Logger) (resRaw string, err error) {
	acc := NewLogAcc(WithLogger(NewLoggerAdapter(logger)))
	acc.Skip(1)
	acc.SetValue("resRaw", &resRaw)
	defer acc.Debug(ctx, "DoRequest")
	defer acc.SetErr(&err)
	acc.Update(map[string]any{"host": "example.test"})
	resRaw = "final response"
	return
}

func TestLogAccDeferredDebugSkip(t *testing.T) {
	var buffer bytes.Buffer
	logger := newBufferedLogger(t, &buffer, Config{Level: slog.LevelDebug})
	_, file, line, _ := runtime.Caller(0)
	response, err := logAccRequest(t.Context(), logger)
	if err != nil {
		t.Fatal(err)
	}
	expected := filepath.Base(file) + ":" + strconv.Itoa(line+1) + " DoRequest"
	if !strings.Contains(buffer.String(), expected) || !strings.Contains(buffer.String(), `"resRaw":"`+response+`"`) {
		t.Fatalf("deferred caller or final response incorrect: %s", buffer.String())
	}
}

func TestLogAccInfo(t *testing.T) {
	ctx := context.WithValue(t.Context(), "traceId", "123321")
	lacc := NewLogAcc(WithCost(time.Now()))
	defer lacc.Info(ctx, "TestLogAccInfo")
	lacc.SetValue("a", 123)
	lacc.Update(map[string]any{
		"b": 456,
		"c": true,
	})
}

func TestLogAccNewInstanceAndStartTime(t *testing.T) {
	var buffer bytes.Buffer
	logger := newBufferedLogger(t, &buffer, Config{})
	start := time.Now().Add(-time.Second)
	loggerBackend := NewLoggerAdapter(logger)
	first := NewLogAcc(WithLogger(loggerBackend), WithCost(start))
	first.SetValue("source", "first")
	first.Skip(2)
	second := NewLogAcc(WithLogger(loggerBackend))
	second.SetValue("source", "second")
	second.Skip(1)
	fields, backend, firstStart, skip := first.snapshot()
	if fields["source"] != "first" || len(fields) != 1 || backend != loggerBackend || firstStart != start || skip != 2 {
		t.Fatalf("first instance changed: %v, %v, %d", fields, firstStart, skip)
	}
	fields, backend, secondStart, skip := second.snapshot()
	if fields["source"] != "second" || len(fields) != 1 || backend != loggerBackend || !secondStart.IsZero() || skip != 1 {
		t.Fatalf("second instance shared state: %v, %v, %d", fields, secondStart, skip)
	}
	acc := NewLogAcc(WithLogger(NewLoggerAdapter(logger)))
	acc.SetStartTime(start)
	acc.Info(t.Context(), "timed")
	if !strings.Contains(buffer.String(), "] timed") {
		t.Fatal(buffer.String())
	}
	buffer.Reset()
	acc.SetStartTime(time.Time{})
	acc.Info(t.Context(), "untimed")
	if !strings.Contains(buffer.String(), " untimed\n") || strings.Contains(buffer.String(), "] untimed") {
		t.Fatal(buffer.String())
	}
}

func TestLogAccSharedContractAndCaller(t *testing.T) {
	var buffer bytes.Buffer
	logger := newBufferedLogger(t, &buffer, Config{Level: slog.LevelDebug})
	var acc adapter.LogAcc = NewLogAcc(WithLogger(NewLoggerAdapter(logger)))
	acc.SetValue("a", 1)
	acc.Update(map[string]any{"b": 2})
	acc.SetStartTime(time.Time{})
	acc.Skip(0)
	err := errors.New("example")
	acc.SetErr(&err)
	_, file, line, _ := runtime.Caller(0)
	acc.Info(t.Context(), "adapted")
	if !strings.Contains(buffer.String(), filepath.Base(file)+":"+strconv.Itoa(line+1)+" adapted") {
		t.Fatal(buffer.String())
	}
	buffer.Reset()
	acc.Debug(t.Context(), "debug")
	acc.Warn(t.Context(), "warn")
	acc.Error(t.Context(), "error")
	if strings.Count(buffer.String(), "\n") != 3 || !strings.Contains(buffer.String(), `"err":"example"`) {
		t.Fatal(buffer.String())
	}
}

func TestLogAccConcurrentStartAndOutput(t *testing.T) {
	var buffer bytes.Buffer
	logger := newBufferedLogger(t, &buffer, Config{})
	acc := NewLogAcc(WithLogger(NewLoggerAdapter(logger)))
	var wg sync.WaitGroup
	for worker := range 4 {
		wg.Go(func() {
			for range 20 {
				acc.SetStartTime(time.Now())
				acc.SetValue("worker", worker)
				requestAcc := NewLogAcc(WithLogger(NewLoggerAdapter(logger)))
				requestAcc.SetStartTime(time.Time{})
				requestAcc.Info(t.Context(), "request")
				acc.Info(t.Context(), "source")
			}
		})
	}
	wg.Wait()
	if strings.Count(buffer.String(), "\n") != 160 {
		t.Fatal("lost concurrent records")
	}
}

func TestLogAccTraceContext(t *testing.T) {
	for _, tc := range []struct {
		name     string
		key      any
		injected bool
	}{
		{name: "default string key", key: "traceId"},
		{name: "configured global key", key: testCtxKey("traceId")},
		{name: "injected logger key", key: testCtxKey("traceId"), injected: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var buffer bytes.Buffer
			config := Config{}
			if tc.name != "default string key" {
				config.TraceKey = tc.key
			}
			logger := newBufferedLogger(t, &buffer, config)
			var acc *LogAcc
			if tc.injected {
				acc = NewLogAcc(WithLogger(NewLoggerAdapter(logger)))
			} else {
				previous := defaultLogger
				defaultLogger = logger
				t.Cleanup(func() { defaultLogger = previous })
				acc = NewLogAcc()
			}
			ctx := context.WithValue(t.Context(), tc.key, "trace-123")
			func() {
				defer acc.Info(ctx, "exit")
				acc.SetValue("result", "done")
			}()
			line := buffer.String()
			if !strings.Contains(line, `{trace-123}`) || !strings.Contains(line, `"result":"done"`) {
				t.Fatalf("ctx or accumulated fields missing: %s", line)
			}
		})
	}
}
