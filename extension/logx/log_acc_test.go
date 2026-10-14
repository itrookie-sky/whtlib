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

func TestLogAccErrorAndDefault(t *testing.T) {
	var buffer bytes.Buffer
	previous := defaultLogger
	defaultLogger = newBufferedLogger(t, &buffer, Config{})
	t.Cleanup(func() { defaultLogger = previous })
	acc := NewLogAcc()
	var err error
	func() { defer acc.SetErr(&err); err = errors.New("final error") }()
	acc.SetErr(nil)
	var empty error
	acc.SetErr(&empty)
	acc.Info(t.Context(), "default")
	if !strings.Contains(buffer.String(), `"err":"final error"`) {
		t.Fatal(buffer.String())
	}
	buffer.Reset()
	var zero LogAcc
	zero.SetValue("zero", true).Info(t.Context(), "zero value")
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
