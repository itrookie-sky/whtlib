package logx

import (
	"bytes"
	"context"
	"log/slog"
	"testing"
	"time"
)

func TestContextTracePlaceholder(t *testing.T) {
	type traceValue string
	for _, tc := range []struct {
		name  string
		trace any
		user  any
		want  string
	}{
		{name: "empty string", trace: "", want: "[INFO] message\n"},
		{name: "named empty string", trace: traceValue(""), user: 1001, want: "[INFO] {\"userId\":1001} message\n"},
		{name: "empty", want: "[INFO] message\n"},
		{name: "trace only", trace: "abc123", want: "[INFO] {abc123} message\n"},
		{name: "user only", user: 1001, want: "[INFO] {\"userId\":1001} message\n"},
		{name: "both", trace: traceValue("abc123"), user: 1001, want: "[INFO] {abc123} {\"userId\":1001} message\n"},
		{name: "empty user", trace: "abc123", user: "", want: "[INFO] {abc123} message\n"},
		{name: "typed nil", trace: (*string)(nil), user: (*int)(nil), want: "[INFO] message\n"},
		{name: "zero values", trace: 0, user: false, want: "[INFO] {0} {\"userId\":false} message\n"},
		{name: "escaped trace", trace: "a\nb", want: "[INFO] {a\\nb} message\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var buffer bytes.Buffer
			logger := newBufferedLogger(t, &buffer, Config{ContextFields: []ContextField{{Name: "userId", Key: "user"}}})
			ctx := t.Context()
			if tc.trace != nil {
				ctx = context.WithValue(ctx, "traceId", tc.trace)
			}
			if tc.user != nil {
				ctx = context.WithValue(ctx, "user", tc.user)
			}
			err := logger.core.handler.Handle(ctx, slog.NewRecord(time.Time{}, slog.LevelInfo, "message", 0))
			if err != nil {
				t.Fatal(err)
			}
			if buffer.String() != tc.want {
				t.Fatalf("want %q, got %q", tc.want, buffer.String())
			}
		})
	}
}
