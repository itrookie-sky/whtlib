package tkcache

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/itrookie-sky/whtlib/toolkit/tkredis"
	"github.com/redis/go-redis/v9"
)

func newTestCache(t *testing.T, backend Backend, namespace string, server *miniredis.Miniredis) (cache *Cache) {
	t.Helper()
	config := Config{Backend: backend, Namespace: namespace}
	if backend == BackendRedis {
		native := redis.NewClient(&redis.Options{Addr: server.Addr(), MaxRetries: -1})
		t.Cleanup(func() { _ = native.Close() })
		var err error
		config.RedisClient, err = tkredis.Wrap(native)
		if err != nil {
			t.Fatal(err)
		}
	}
	var err error
	cache, err = New(config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cache.Close() })
	return
}

func eachBackend(t *testing.T, test func(t *testing.T, cache *Cache, advance func(time.Duration))) {
	t.Helper()
	for _, backend := range []Backend{BackendMemory, BackendRedis} {
		t.Run(string(backend), func(t *testing.T) {
			var server *miniredis.Miniredis
			advance := time.Sleep
			if backend == BackendRedis {
				server = miniredis.RunT(t)
				advance = server.FastForward
			}
			test(t, newTestCache(t, backend, "test", server), advance)
		})
	}
}

func mustSet(t *testing.T, cache *Cache, key, value string, ttl time.Duration) {
	t.Helper()
	err := cache.SetString(t.Context(), key, value, ttl)
	if err != nil {
		t.Fatal(err)
	}
}

func wantValue(t *testing.T, cache *Cache, key, expected string, expectedFound bool) {
	t.Helper()
	value, found, err := cache.GetString(t.Context(), key)
	if err != nil || found != expectedFound || value != expected {
		t.Fatalf("Get(%q) = %q,%v,%v; want %q,%v", key, value, found, err, expected, expectedFound)
	}
}

func TestBytesTTLAndAtomic(t *testing.T) {
	eachBackend(t, func(t *testing.T, cache *Cache, advance func(time.Duration)) {
		ctx := t.Context()
		wantValue(t, cache, "missing", "", false)
		ttl, found, err := cache.TTL(ctx, "missing")
		if err != nil || found || ttl != 0 {
			t.Fatalf("缺失 TTL: %v,%v,%v", ttl, found, err)
		}
		mustSet(t, cache, "empty", "", 0)
		wantValue(t, cache, "empty", "", true)
		ttl, found, err = cache.TTL(ctx, "empty")
		if err != nil || !found || ttl != 0 {
			t.Fatalf("永久 TTL: %v,%v,%v", ttl, found, err)
		}
		input := []byte("original")
		err = cache.Set(ctx, "copy", input, 0)
		if err != nil {
			t.Fatal(err)
		}
		input[0] = 'X'
		output, _, err := cache.Get(ctx, "copy")
		if err != nil {
			t.Fatal(err)
		}
		output[0] = 'Y'
		wantValue(t, cache, "copy", "original", true)
		mustSet(t, cache, "expire", "old", 50*time.Millisecond)
		ttl, found, err = cache.TTL(ctx, "expire")
		if err != nil || !found || ttl <= 0 || ttl > 50*time.Millisecond {
			t.Fatalf("正数 TTL: %v,%v,%v", ttl, found, err)
		}
		advance(60 * time.Millisecond)
		wantValue(t, cache, "expire", "", false)
		mustSet(t, cache, "expire", "persist", time.Second)
		_, found, err = cache.GetAndExpire(ctx, "expire", 0)
		if err != nil || !found {
			t.Fatalf("清除 TTL: %v", err)
		}
		ttl, found, err = cache.TTL(ctx, "expire")
		if err != nil || !found || ttl != 0 {
			t.Fatalf("续期为永久: %v,%v,%v", ttl, found, err)
		}
		swapped, err := cache.CompareAndSwap(ctx, "expire", []byte("wrong"), []byte("new"), 0)
		if err != nil || swapped {
			t.Fatalf("错误旧值不应更新: %v", err)
		}
		swapped, err = cache.CompareAndSwap(ctx, "expire", []byte("persist"), []byte("new"), time.Second)
		if err != nil || !swapped {
			t.Fatalf("匹配更新失败: %v", err)
		}
		deleted, err := cache.CompareAndDelete(ctx, "expire", []byte("persist"))
		if err != nil || deleted {
			t.Fatalf("旧会话不应删除新值: %v", err)
		}
		deleted, err = cache.CompareAndDelete(ctx, "expire", []byte("new"))
		if err != nil || !deleted {
			t.Fatalf("匹配删除失败: %v", err)
		}
		mustSet(t, cache, "hit", "existing", time.Second)
		value, err := cache.GetOrSet(ctx, "hit", []byte("candidate"), 0)
		if err != nil || string(value) != "existing" {
			t.Fatalf("GetOrSet 命中: %q,%v", value, err)
		}
		ttl, _, err = cache.TTL(ctx, "hit")
		if err != nil || ttl <= 0 {
			t.Fatalf("命中不能改变原 TTL: %v,%v", ttl, err)
		}
		value, found, err = cache.GetAndDelete(ctx, "hit")
		if err != nil || !found || string(value) != "existing" {
			t.Fatalf("读后删除: %q,%v,%v", value, found, err)
		}
		wantValue(t, cache, "hit", "", false)
		err = cache.SetMap(ctx, map[string][]byte{"a": []byte("A"), "b": {}}, 0)
		if err != nil {
			t.Fatal(err)
		}
		values, err := cache.GetMap(ctx, []string{"a", "b", "missing"})
		if err != nil || len(values) != 2 || string(values["a"]) != "A" {
			t.Fatalf("批量读取: %v,%v", values, err)
		}
	})
}

func TestJSONAndEmptyPolicy(t *testing.T) {
	eachBackend(t, func(t *testing.T, cache *Cache, _ func(time.Duration)) {
		type user struct {
			ID int64 `json:"id"`
		}
		err := cache.SetJSON(t.Context(), "user", &user{ID: 9007199254740993}, 0)
		if err != nil {
			t.Fatal(err)
		}
		value, found, err := GetJSON[*user](t.Context(), cache, "user")
		if err != nil || !found || value.ID != 9007199254740993 {
			t.Fatalf("结构体解码: %+v,%v,%v", value, found, err)
		}
		dynamic, _, err := GetJSON[map[string]any](t.Context(), cache, "user")
		if err != nil || dynamic["id"] != json.Number("9007199254740993") {
			t.Fatalf("动态数字精度丢失: %v,%v", dynamic, err)
		}
		for _, bad := range []string{"broken", "{} {}"} {
			mustSet(t, cache, "bad", bad, 0)
			_, found, err = GetJSON[user](t.Context(), cache, "bad")
			if !found || err == nil {
				t.Fatal("损坏 JSON 应保留命中标志并返回错误")
			}
		}
		for _, keepEmpty := range []bool{true, false} {
			key := fmt.Sprint("nil-", keepEmpty)
			value, err = GetOrLoadJSON(t.Context(), cache, key, 0, func(context.Context) (value *user, err error) { return }, WithCacheEmpty(keepEmpty))
			if err != nil || value != nil {
				t.Fatalf("typed nil 回源失败: %v", err)
			}
			_, found, err = cache.Get(t.Context(), key)
			if err != nil || found != keepEmpty {
				t.Fatalf("空值策略错误: found=%v,keep=%v,err=%v", found, keepEmpty, err)
			}
		}
		err = cache.SetJSON(t.Context(), "user", make(chan int), 0)
		if err == nil {
			t.Fatal("不可编码值应报错")
		}
		value, _, err = GetJSON[*user](t.Context(), cache, "user")
		if err != nil || value.ID != 9007199254740993 {
			t.Fatal("编码失败不能覆盖原缓存")
		}
	})
}

func TestAtomicContendersAndNamespace(t *testing.T) {
	eachBackend(t, func(t *testing.T, cache *Cache, _ func(time.Duration)) {
		var winners atomic.Int32
		var workers sync.WaitGroup
		for range 24 {
			workers.Go(func() {
				ok, err := cache.SetIfAbsent(t.Context(), "race", []byte("win"), 0)
				if err != nil {
					t.Error(err)
				}
				if ok {
					winners.Add(1)
				}
			})
		}
		workers.Wait()
		if winners.Load() != 1 {
			t.Fatalf("NX 应恰有一个成功者: %d", winners.Load())
		}
		ok, err := cache.Once(t.Context(), "race", time.Second)
		if err != nil || !ok {
			t.Fatalf("Once 不应与 data 冲突: %v", err)
		}
		ok, err = cache.Once(t.Context(), "race", time.Second)
		if err != nil || ok {
			t.Fatal("占位不能重复获取")
		}
		err = cache.Clear(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		wantValue(t, cache, "race", "", false)
		ok, err = cache.Once(t.Context(), "race", time.Second)
		if err != nil || !ok {
			t.Fatal("Clear 应清理 Once 区域")
		}
	})
	server := miniredis.RunT(t)
	first := newTestCache(t, BackendRedis, "one", server)
	shared := newTestCache(t, BackendRedis, "one", server)
	other := newTestCache(t, BackendRedis, "two", server)
	mustSet(t, first, "key", "shared", 0)
	mustSet(t, other, "key", "other", 0)
	wantValue(t, shared, "key", "shared", true)
	err := first.Clear(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	wantValue(t, other, "key", "other", true)
	wantValue(t, shared, "key", "", false)
	err = first.Close()
	if err != nil {
		t.Fatal(err)
	}
	raw := first.backend.(*redisStore).client
	err = raw.Ping(t.Context()).Err()
	if err != nil {
		t.Fatalf("关闭缓存不能关闭借用客户端: %v", err)
	}
	memory := newTestCache(t, BackendMemory, "same", nil)
	separate := newTestCache(t, BackendMemory, "same", nil)
	mustSet(t, memory, "key", "private", 0)
	wantValue(t, separate, "key", "", false)
}

func TestInvalidArgumentsAndLifecycle(t *testing.T) {
	for _, config := range []Config{{}, {Namespace: "*"}, {Namespace: "ok", Backend: "bad"}, {Namespace: "ok", Backend: BackendRedis}} {
		_, err := New(config)
		if !errors.Is(err, ErrInvalidConfig) {
			t.Fatalf("错误配置应拒绝: %+v,%v", config, err)
		}
	}
	for _, ttl := range []time.Duration{time.Nanosecond, time.Millisecond + 1} {
		value, err := normalizeTTL(ttl)
		if err != nil || value < ttl || value%time.Millisecond != 0 || value-ttl >= time.Millisecond {
			t.Fatalf("毫秒向上取整: %v -> %v,%v", ttl, value, err)
		}
	}
	eachBackend(t, func(t *testing.T, cache *Cache, _ func(time.Duration)) {
		err := cache.Set(t.Context(), "key", nil, -1)
		if !errors.Is(err, ErrInvalidArgument) {
			t.Fatal(err)
		}
		_, _, err = cache.Get(t.Context(), "")
		if !errors.Is(err, ErrInvalidArgument) {
			t.Fatal(err)
		}
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		err = cache.Set(ctx, "key", nil, 0)
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
		err = cache.Close()
		if err != nil {
			t.Fatal(err)
		}
		err = cache.Close()
		if err != nil {
			t.Fatal(err)
		}
		_, _, err = cache.Get(t.Context(), "key")
		if !errors.Is(err, ErrClosed) {
			t.Fatalf("关闭后应拒绝操作: %v", err)
		}
	})
}
