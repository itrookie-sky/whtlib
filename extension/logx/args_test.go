package logx

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
)

func TestPlainArguments(t *testing.T) {
	var buffer bytes.Buffer
	logger := newBufferedLogger(t, &buffer, Config{Level: slog.LevelDebug})
	previous := defaultLogger
	defaultLogger = logger
	defer func() { defaultLogger = previous }()
	methods := []func(context.Context, string, ...any){
		logger.Debug, logger.Info, logger.Warn, logger.Error,
		func(ctx context.Context, msg string, args ...any) { logger.Log(ctx, slog.LevelInfo, msg, args...) },
		Debug, Info, Warn, Error,
		func(ctx context.Context, msg string, args ...any) { Log(ctx, slog.LevelInfo, msg, args...) },
	}
	for _, method := range methods {
		for _, tc := range []struct {
			message string
			args    []any
			want    string
		}{
			{message: "订单", args: []any{123, "完成"}, want: "订单 123 完成"},
			{message: "值", args: []any{"single", nil, errors.New("failed"), slog.Int("count", 2)}, want: "值 single <nil> failed count=2"},
			{message: "原文 %s", want: "原文 %s"},
			{args: []any{1, 2}, want: "1 2"},
			{message: "多行", args: []any{"a\nb"}, want: "多行 a\\nb"},
		} {
			buffer.Reset()
			method(t.Context(), tc.message, tc.args...)
			output := buffer.String()
			if !strings.HasSuffix(output, " "+tc.want+"\n") || strings.Contains(output, "!BADKEY") || strings.Count(output, "\n") != 1 {
				t.Fatalf("want body %q, got %q", tc.want, output)
			}
		}
	}
	for _, method := range []func(context.Context, string, ...any){
		logger.Debugf, logger.Infof, logger.Warnf, logger.Errorf, Debugf, Infof, Warnf, Errorf,
	} {
		buffer.Reset()
		method(t.Context(), "订单=%d %s", 123, "完成")
		if !strings.HasSuffix(buffer.String(), " 订单=123 完成\n") {
			t.Fatalf("formatted output: %q", buffer.String())
		}
	}
	buffer.Reset()
	NewLogAcc(WithLogger(NewLoggerAdapter(logger))).Info(t.Context(), "订单", "id", 123, "state", "完成")
	if !strings.HasSuffix(buffer.String(), " 订单 {\"id\":123,\"state\":\"完成\"}\n") {
		t.Fatalf("structured output: %q", buffer.String())
	}
}

type countedStringer struct{ calls *int }

func (s countedStringer) String() (value string) {
	*s.calls++
	return "value"
}

func TestFilteredPlainArguments(t *testing.T) {
	var buffer bytes.Buffer
	logger := newBufferedLogger(t, &buffer, Config{})
	calls := 0
	logger.Debug(t.Context(), "filtered", countedStringer{calls: &calls})
	if calls != 0 || buffer.Len() != 0 {
		t.Fatalf("filtered arguments formatted: calls=%d output=%q", calls, buffer.String())
	}
}
