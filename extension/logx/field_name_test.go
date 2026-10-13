package logx

import (
	"context"
	"testing"
)

func TestContextFieldHyphens(t *testing.T) {
	for _, name := range []string{"user-id", "x-request-id", "-user", "user-", "-"} {
		t.Run(name, func(t *testing.T) {
			config, err := BuildDefaultConfig(Config{ContextFields: []ContextField{{Name: name, Key: "user"}}})
			if err != nil {
				t.Fatal(err)
			}
			data, err := contextJSON(context.WithValue(t.Context(), "user", 42), config)
			if err != nil {
				t.Fatal(err)
			}
			if string(data) != `{"`+name+`":42}` {
				t.Fatalf("unexpected fields: %s", data)
			}
		})
	}
	for _, name := range []string{"", "1user", "user id", "user.id", "traceId"} {
		_, err := BuildDefaultConfig(Config{ContextFields: []ContextField{{Name: name, Key: "user"}}})
		if err == nil {
			t.Fatalf("invalid field %q accepted", name)
		}
	}
	_, err := BuildDefaultConfig(Config{ContextFields: []ContextField{{Name: "user-id", Key: "first"}, {Name: "user-id", Key: "second"}}})
	if err == nil {
		t.Fatal("duplicate hyphenated field accepted")
	}
}
