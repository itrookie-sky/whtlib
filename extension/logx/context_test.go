package logx

import (
	"context"
	"strings"
	"testing"
)

func TestContextText(t *testing.T) {
	type text string
	config, err := BuildDefaultConfig(Config{ContextFields: []ContextField{
		{Name: "userId", Key: "user"}, {Name: "enabled", Key: "enabled"},
		{Name: "empty", Key: "empty"}, {Name: "nil", Key: "nil"}, {Name: "detail", Key: "detail"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.WithValue(t.Context(), "traceId", text("abc123"))
	ctx = context.WithValue(ctx, "user", 1001)
	ctx = context.WithValue(ctx, "enabled", false)
	ctx = context.WithValue(ctx, "empty", "")
	ctx = context.WithValue(ctx, "nil", (*int)(nil))
	ctx = context.WithValue(ctx, "detail", map[string]int{"count": 2})
	data, err := contextText(ctx, config)
	if err != nil || string(data) != `{traceId:abc123,userId:1001,enabled:false,detail:{"count":2}}` {
		t.Fatalf("data=%s err=%v", data, err)
	}
	data, err = contextText(t.Context(), config)
	if err != nil || string(data) != "{}" {
		t.Fatalf("empty: %s %v", data, err)
	}
	data, err = contextText(context.WithValue(t.Context(), "traceId", "a\n\r\"b"), config)
	if err != nil || string(data) != `{traceId:a\n\r\"b}` || strings.ContainsAny(string(data), "\n\r") {
		t.Fatalf("escaped: %s %v", data, err)
	}
	ctx = context.WithValue(ctx, "traceId", make(chan int))
	data, err = contextText(ctx, config)
	if err == nil || !strings.HasPrefix(string(data), "{traceId:[json encode error],userId:1001,") {
		t.Fatalf("error fallback: %s %v", data, err)
	}
}
