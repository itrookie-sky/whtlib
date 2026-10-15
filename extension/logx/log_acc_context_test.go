package logx

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

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
				acc = NewLogAcc(WithLogger(logger))
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
