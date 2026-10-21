package tkredis

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

func newTestClient(t *testing.T) (client *Client, server *miniredis.Miniredis) {
	t.Helper()
	server = miniredis.RunT(t)
	client, err := New(&redis.Options{
		Addr: server.Addr(), MaxRetries: -1,
		DialTimeout: time.Second, ReadTimeout: time.Second, WriteTimeout: time.Second,
		ContextTimeoutEnabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		err := client.Close()
		if err != nil {
			t.Error(err)
		}
	})
	return
}

func TestDoRawValuesAndExplicitJSON(t *testing.T) {
	client, _ := newTestClient(t)
	ctx := t.Context()
	_, err := client.Do(ctx, "GET", "missing")
	if !errors.Is(err, redis.Nil) {
		t.Fatalf("missing error = %v", err)
	}
	_, err = client.Do(ctx, "SET", "empty", "")
	if err != nil {
		t.Fatal(err)
	}
	value, err := client.Do(ctx, "GET", "empty")
	if err != nil || value != "" {
		t.Fatalf("empty value = %#v, error = %v", value, err)
	}
	value, err = client.Do(ctx, "SMEMBERS", "missing")
	if err != nil || !reflect.DeepEqual(value, []any{}) {
		t.Fatalf("empty collection = %#v, error = %v", value, err)
	}
	payload := map[string]any{"count": 9007199254740993}
	args := []any{"json", JSON(payload)}
	_, err = client.Do(ctx, "SET", args...)
	if err != nil {
		t.Fatal(err)
	}
	_, unchanged := args[1].(JSONValue)
	if !unchanged {
		t.Fatal("Do modified caller argument slice")
	}
	value, err = client.Do(ctx, "GET", "json")
	if err != nil || value != `{"count":9007199254740993}` {
		t.Fatalf("JSON = %v, error = %v", value, err)
	}
	_, err = client.Do(ctx, "SET", "unsupported", payload)
	if err == nil {
		t.Fatal("Do should require explicit JSON for maps")
	}
	_, err = client.DoJSON(ctx, "SET", "converted", payload)
	if err != nil {
		t.Fatal(err)
	}
}

func TestEncodeArgsCopiesInputAndHandlesNil(t *testing.T) {
	var nilMap map[string]int
	args := []any{"SET", "key", map[string]int{"a": 1}, []byte("raw"), nilMap, nil}
	converted, err := EncodeArgs(2, args...)
	if err != nil {
		t.Fatal(err)
	}
	_, unchanged := args[2].(map[string]int)
	if !unchanged || string(converted[2].([]byte)) != `{"a":1}` || string(converted[3].([]byte)) != "raw" || string(converted[4].([]byte)) != "null" || converted[5] != nil {
		t.Fatalf("unexpected arguments: %#v", converted)
	}
	_, err = EncodeArgs(0, map[string]any{"bad": make(chan int)})
	if err == nil {
		t.Fatal("unsupported JSON must fail")
	}
	_, err = EncodeArgs(-1, "a")
	if err == nil {
		t.Fatal("negative index must fail")
	}
}

func TestPipelineRetainsPartialResults(t *testing.T) {
	client, _ := newTestClient(t)
	ctx := t.Context()
	commands := [][]any{
		{"SET", "plain", "value"},
		{"LPUSH", "plain", "bad"},
		{"GET", "missing"},
		{"GET", "plain"},
	}
	results, err := client.Pipeline(ctx, commands...)
	if err == nil || len(results) != 4 {
		t.Fatalf("pipeline results = %#v, error = %v", results, err)
	}
	if results[0].Data != "OK" || results[1].Err == nil || !results[2].IsNil() || results[3].Data != "value" {
		t.Fatalf("partial results lost: %#v", results)
	}
	if (Result{Data: ""}).IsNil() || (Result{Data: []any{}}).IsNil() {
		t.Fatal("empty values must remain distinct from redis.Nil")
	}
	results, err = client.Pipeline(ctx)
	if err != nil || len(results) != 0 {
		t.Fatalf("empty pipeline = %#v, %v", results, err)
	}
}

func TestScriptsReloadAndRegisterConcurrently(t *testing.T) {
	client, _ := newTestClient(t)
	ctx := t.Context()
	err := client.ScriptLoad(ctx, "echo", "return ARGV[1]")
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.Do(ctx, "SCRIPT", "FLUSH")
	if err != nil {
		t.Fatal(err)
	}
	value, err := client.EvalSha(ctx, "echo", nil, "after-flush")
	if err != nil || value != "after-flush" {
		t.Fatalf("reload result = %v, %v", value, err)
	}
	var group sync.WaitGroup
	for range 20 {
		group.Go(func() {
			err := client.RegisterScript("concurrent", "return ARGV[1]")
			if err != nil {
				t.Error(err)
				return
			}
			value, err := client.EvalSha(ctx, "concurrent", nil, "ok")
			if err != nil || value != "ok" {
				t.Errorf("concurrent result = %v, %v", value, err)
			}
		})
	}
	group.Wait()
	_, err = client.EvalSha(ctx, "missing", nil)
	if !errors.Is(err, ErrScriptNotFound) {
		t.Fatalf("missing script error = %v", err)
	}
	path := filepath.Join(t.TempDir(), "from_file.lua")
	err = os.WriteFile(path, []byte("return 9"), 0o600)
	if err != nil {
		t.Fatal(err)
	}
	err = client.ScriptLoadByPath(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	value, err = client.EvalSha(ctx, "from_file", nil)
	if err != nil || value != int64(9) {
		t.Fatalf("file script result = %v, %v", value, err)
	}
}

func TestAtomicHelpers(t *testing.T) {
	client, server := newTestClient(t)
	ctx := t.Context()
	var successes atomic.Int64
	var group sync.WaitGroup
	for range 32 {
		group.Go(func() {
			ok, err := client.Once(ctx, "once", 150*time.Millisecond)
			if err != nil {
				t.Error(err)
				return
			}
			if ok {
				successes.Add(1)
			}
		})
	}
	group.Wait()
	if successes.Load() != 1 || server.TTL("once") != 150*time.Millisecond {
		t.Fatalf("successes=%d, ttl=%v", successes.Load(), server.TTL("once"))
	}
	server.FastForward(151 * time.Millisecond)
	ready, err := client.CD(ctx, "once")
	if err != nil || !ready {
		t.Fatalf("cooldown after expiry = %v, %v", ready, err)
	}
	_, err = client.Do(ctx, "SET", "conditional", "owner")
	if err != nil {
		t.Fatal(err)
	}
	ok, err := client.Cad(ctx, "conditional", "other")
	if err != nil || ok {
		t.Fatalf("foreign compare delete = %v, %v", ok, err)
	}
	ok, err = client.Cad(ctx, "conditional", "owner")
	if err != nil || !ok {
		t.Fatalf("own compare delete = %v, %v", ok, err)
	}
	for _, want := range []int{0, 1, 2, 0} {
		index, err := client.Polling(ctx, "poll", 3)
		if err != nil || index != want {
			t.Fatalf("poll = %d, want = %d, error = %v", index, want, err)
		}
	}
}

func TestCloseOwnershipAndIdempotency(t *testing.T) {
	server := miniredis.RunT(t)
	native := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { _ = native.Close() })
	client, err := Wrap(native)
	if err != nil || client.Raw() != native {
		t.Fatalf("wrap = %v, %v", client, err)
	}
	var group sync.WaitGroup
	for range 10 {
		group.Go(func() {
			err := client.Close()
			if err != nil {
				t.Error(err)
			}
		})
	}
	group.Wait()
	err = native.Ping(t.Context()).Err()
	if err != nil {
		t.Fatalf("wrapped native client was closed: %v", err)
	}
	_, err = client.Do(t.Context(), "PING")
	if !errors.Is(err, ErrClosed) {
		t.Fatalf("closed command error = %v", err)
	}
	var typedNil *redis.Client
	_, err = Wrap(typedNil)
	if !errors.Is(err, ErrInvalidClient) {
		t.Fatalf("typed nil wrapper error = %v", err)
	}
	owned, err := New(&redis.Options{Addr: server.Addr()})
	if err != nil {
		t.Fatal(err)
	}
	err = owned.Close()
	if err != nil {
		t.Fatal(err)
	}
	err = owned.Raw().Ping(context.Background()).Err()
	if err == nil {
		t.Fatal("owned native client remained open")
	}
}
