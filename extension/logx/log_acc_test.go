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
)

func TestLogAccOverwriteAndSnapshot(t *testing.T) {
	var buffer bytes.Buffer
	logger := newBufferedLogger(t, &buffer, Config{})
	acc := NewLogAcc(WithLogger(logger))
	if acc.SetValue("one", 1) != acc || acc.Update(map[string]any{"one": 2, "two": true}) != acc {
		t.Fatal("identity changed")
	}
	source := map[string]any{"three": "before"}
	acc.Update(source)
	source["three"] = "after"
	acc.Info(t.Context(), "first", "one", 3, "nil", nil)
	acc.Info(t.Context(), "second")
	for _, line := range strings.Split(strings.TrimSpace(buffer.String()), "\n") {
		if !strings.Contains(line, `{"nil":null,"one":3,"three":"before","two":true}`) {
			t.Fatal(line)
		}
	}
	fields, _, _, _ := acc.snapshot(nil)
	fields["one"] = "outside"
	acc.Info(t.Context(), "isolated")
	if strings.Contains(buffer.String(), "outside") {
		t.Fatal("snapshot exposed internal map")
	}
}

// logAccHandle 模拟 lib 的函数级日志：先注册打印，再注册最终错误采集。
func logAccHandle(ctx context.Context, handle func() (string, error), ops ...LogAccOptionFunc) (resRaw string, err error) {
	acc := NewLogAcc(ops...)
	defer acc.Info(ctx, "HandleDevice", "resRaw", &resRaw)
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
					ops = append(ops, WithLogger(logger))
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
				_, body, ok := strings.Cut(line, " INFO [")
				if !ok {
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
	acc := NewLogAcc(WithLogger(logger))
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
	acc.SetValue("zero", true).Info(t.Context(), "zero value")
	if !strings.Contains(buffer.String(), `"zero":true`) {
		t.Fatal(buffer.String())
	}
}

func TestLogAccCallersAndCost(t *testing.T) {
	var buffer bytes.Buffer
	logger := newBufferedLogger(t, &buffer, Config{Level: slog.LevelDebug})
	acc := NewLogAcc(WithLogger(logger))
	_, file, line, _ := runtime.Caller(0)
	acc.Info(t.Context(), "direct")
	if !strings.Contains(buffer.String(), filepath.Base(file)+":"+strconv.Itoa(line+1)+" INFO direct") {
		t.Fatal(buffer.String())
	}
	buffer.Reset()
	acc.Skip(1)
	_, _, line, _ = runtime.Caller(0)
	logAccWrapped(t.Context(), acc)
	if !strings.Contains(buffer.String(), filepath.Base(file)+":"+strconv.Itoa(line+1)+" INFO wrapped") {
		t.Fatal(buffer.String())
	}
	buffer.Reset()
	cost := NewLogAcc(WithLogger(logger), WithCost(time.Now().Add(-time.Second)))
	cost.Info(t.Context(), "cost")
	if !strings.Contains(buffer.String(), " INFO [") || !strings.Contains(buffer.String(), "] cost") {
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
	acc := NewLogAcc(WithLogger(logger))
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
		_, data, ok := strings.Cut(line, " INFO concurrent ")
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

func TestLogAccInvalidPairsAreAtomic(t *testing.T) {
	acc := NewLogAcc()
	for _, args := range [][]any{{"one"}, {"one", 1, 2, 3}} {
		func() {
			defer func() {
				if recover() == nil {
					t.Error("invalid pairs accepted")
				}
			}()
			acc.snapshot(args)
		}()
	}
	fields, _, _, _ := acc.snapshot(nil)
	if len(fields) != 0 {
		t.Fatal("invalid batch partially applied")
	}
}

// logAccRequest 对齐 libclient.DoRequest 的延迟 Debug、Skip 和返回值指针用法。
func logAccRequest(ctx context.Context, logger *Logger) (resRaw string, err error) {
	acc := NewLogAcc(WithLogger(logger))
	defer acc.Skip(1).Debug(ctx, "DoRequest", "resRaw", &resRaw)
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
	expected := filepath.Base(file) + ":" + strconv.Itoa(line+1) + " DEBUG DoRequest"
	if !strings.Contains(buffer.String(), expected) || !strings.Contains(buffer.String(), `"resRaw":"`+response+`"`) {
		t.Fatalf("deferred caller or final response incorrect: %s", buffer.String())
	}
}
