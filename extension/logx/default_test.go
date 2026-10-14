package logx

import (
	"context"
	"testing"
)

func TestLogDefault(t *testing.T) {
	traceKey := CtxKey("traceId")
	ctx := context.WithValue(context.TODO(), traceKey, "321")
	SetConfig(Config{
		TraceKey: traceKey,
	})
	Info(ctx, "TestLogDefault:", 1, 2, 3, 4)
}
