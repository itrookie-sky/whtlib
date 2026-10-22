package tkcache

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/itrookie-sky/whtlib/toolkit/tkredis"

	"github.com/alicebob/miniredis/v2"
)

func wantTTL(t *testing.T, cache *Cache, key string, want time.Duration, found bool) {
	t.Helper()
	ttl, exists, err := cache.TTL(t.Context(), key)
	if err != nil || exists != found || ttl != want {
		t.Fatalf("TTL(%q): got %v, %v, %v; want %v, %v", key, ttl, exists, err, want, found)
	}
}

func TestRedisTTLStates(t *testing.T) {
	if NoExpiration != -1 || TTLNotFound != -2 {
		t.Fatal("TTL 哨兵必须为原始 -1、-2，不能缩放到秒或毫秒")
	}
	eachStore(t, func(t *testing.T, cache *Cache, advance func(time.Duration)) {
		ctx := t.Context()
		wantTTL(t, cache, "missing", TTLNotFound, false)
		mustSet(t, cache, "key", "persistent", NoExpiration)
		wantTTL(t, cache, "key", NoExpiration, true)
		advance(20 * time.Millisecond)
		wantValue(t, cache, "key", "persistent", true)
		wantTTL(t, cache, "key", NoExpiration, true)
		err := cache.Set(ctx, "key", []byte("immediate"), 0)
		if err != nil {
			t.Fatal(err)
		}
		wantValue(t, cache, "key", "", false)
		wantTTL(t, cache, "key", TTLNotFound, false)
		mustSet(t, cache, "key", "temporary", 20*time.Millisecond)
		ttl, found, err := cache.TTL(ctx, "key")
		if err != nil || !found || ttl < 0 || ttl > 20*time.Millisecond {
			t.Fatal("正 TTL 查询错误", ttl, found, err)
		}
		advance(30 * time.Millisecond)
		wantTTL(t, cache, "key", TTLNotFound, false)
	})
}

func TestZeroTTLAtomicOperations(t *testing.T) {
	eachStore(t, func(t *testing.T, cache *Cache, _ func(time.Duration)) {
		ctx := t.Context()
		mustSet(t, cache, "key", "old", NoExpiration)
		stored, err := cache.SetIfAbsent(ctx, "key", []byte("candidate"), 0)
		if err != nil || stored {
			t.Fatal("未满足 NX 条件时不能删除已有值", err)
		}
		wantValue(t, cache, "key", "old", true)
		for range 2 {
			stored, err = cache.SetIfAbsent(ctx, "missing", []byte("candidate"), 0)
			if err != nil || !stored {
				t.Fatal("零 TTL 条件写入应成功但不留下键", err)
			}
			wantTTL(t, cache, "missing", TTLNotFound, false)
		}
		swapped, err := cache.CompareAndSwap(ctx, "key", []byte("wrong"), []byte("candidate"), 0)
		if err != nil || swapped {
			t.Fatal("未匹配的 CAS 不能删除已有值", err)
		}
		wantValue(t, cache, "key", "old", true)
		swapped, err = cache.CompareAndSwap(ctx, "key", []byte("old"), []byte("candidate"), 0)
		if err != nil || !swapped {
			t.Fatal("匹配的零 TTL CAS 应成功", err)
		}
		wantTTL(t, cache, "key", TTLNotFound, false)
		swapped, err = cache.CompareAndSwap(ctx, "missing", nil, []byte("candidate"), 0)
		if err != nil || swapped {
			t.Fatal("CAS 不能创建不存在的键", err)
		}
		mustSet(t, cache, "key", "old", NoExpiration)
		value, found, err := cache.GetAndExpire(ctx, "key", 0)
		if err != nil || !found || string(value) != "old" {
			t.Fatal("零 TTL 读后过期应返回旧值", err)
		}
		wantTTL(t, cache, "key", TTLNotFound, false)
		_, found, err = cache.GetAndExpire(ctx, "key", 0)
		if err != nil || found {
			t.Fatal("零 TTL 读后过期不能创建键", err)
		}
		mustSet(t, cache, "key", "old", NoExpiration)
		value, err = cache.GetOrSet(ctx, "key", []byte("candidate"), 0)
		if err != nil || string(value) != "old" {
			t.Fatal("GetOrSet 命中应返回旧值", err)
		}
		wantTTL(t, cache, "key", NoExpiration, true)
		value, err = cache.GetOrSet(ctx, "missing", []byte("candidate"), 0)
		if err != nil || string(value) != "candidate" {
			t.Fatal("GetOrSet 未命中应返回候选值", err)
		}
		wantTTL(t, cache, "missing", TTLNotFound, false)
		err = cache.SetMap(ctx, map[string][]byte{"key": []byte("new"), "missing": []byte("new")}, 0)
		if err != nil {
			t.Fatal(err)
		}
		wantTTL(t, cache, "key", TTLNotFound, false)
		wantTTL(t, cache, "missing", TTLNotFound, false)
	})
}

func TestPermanentTTLAtomicOperations(t *testing.T) {
	eachStore(t, func(t *testing.T, cache *Cache, _ func(time.Duration)) {
		ctx := t.Context()
		stored, err := cache.SetIfAbsent(ctx, "nx", nil, NoExpiration)
		if err != nil || !stored {
			t.Fatal(err)
		}
		wantTTL(t, cache, "nx", NoExpiration, true)
		mustSet(t, cache, "key", "old", time.Hour)
		_, found, err := cache.GetAndExpire(ctx, "key", NoExpiration)
		if err != nil || !found {
			t.Fatal(err)
		}
		wantTTL(t, cache, "key", NoExpiration, true)
		mustSet(t, cache, "key", "old", time.Hour)
		swapped, err := cache.CompareAndSwap(ctx, "key", []byte("old"), []byte("new"), NoExpiration)
		if err != nil || !swapped {
			t.Fatal(err)
		}
		wantTTL(t, cache, "key", NoExpiration, true)
		_, err = cache.GetOrSet(ctx, "getset", []byte("new"), NoExpiration)
		if err != nil {
			t.Fatal(err)
		}
		wantTTL(t, cache, "getset", NoExpiration, true)
		err = cache.SetMap(ctx, map[string][]byte{"key": {}, "batch": {}}, NoExpiration)
		if err != nil {
			t.Fatal(err)
		}
		wantTTL(t, cache, "key", NoExpiration, true)
		wantTTL(t, cache, "batch", NoExpiration, true)
	})
}

func TestTTLRejectsUnsupportedNegativeValues(t *testing.T) {
	eachStore(t, func(t *testing.T, cache *Cache, _ func(time.Duration)) {
		ctx := t.Context()
		mustSet(t, cache, "key", "old", NoExpiration)
		for _, ttl := range []time.Duration{TTLNotFound, -time.Millisecond, -time.Second} {
			tests := []struct {
				name string
				run  func() (err error)
			}{
				{name: "Set", run: func() (err error) { return cache.Set(ctx, "key", nil, ttl) }},
				{name: "SetIfAbsent", run: func() (err error) { _, err = cache.SetIfAbsent(ctx, "new", nil, ttl); return }},
				{name: "GetAndExpire", run: func() (err error) { _, _, err = cache.GetAndExpire(ctx, "key", ttl); return }},
				{name: "CompareAndSwap", run: func() (err error) { _, err = cache.CompareAndSwap(ctx, "key", []byte("old"), nil, ttl); return }},
				{name: "GetOrSet", run: func() (err error) { _, err = cache.GetOrSet(ctx, "new", nil, ttl); return }},
				{name: "SetMap", run: func() (err error) { return cache.SetMap(ctx, map[string][]byte{"key": nil}, ttl) }},
				{name: "GetOrLoad", run: func() (err error) {
					_, err = cache.GetOrLoad(ctx, "new", ttl, func(context.Context) (value []byte, err error) {
						t.Error("无效 TTL 不应触发回源")
						return
					})
					return
				}},
			}
			for _, test := range tests {
				err := test.run()
				if !errors.Is(err, ErrInvalidArgument) {
					t.Fatalf("%s 应拒绝 TTL %v: %v", test.name, ttl, err)
				}
				wantValue(t, cache, "key", "old", true)
				wantTTL(t, cache, "key", NoExpiration, true)
			}
		}
	})
}

func TestZeroTTLLoadDoesNotCache(t *testing.T) {
	eachStore(t, func(t *testing.T, cache *Cache, _ func(time.Duration)) {
		loads := 0
		for range 2 {
			value, err := cache.GetOrLoad(t.Context(), "load", 0, func(context.Context) (value []byte, err error) {
				loads++
				return []byte("loaded"), nil
			})
			if err != nil || string(value) != "loaded" {
				t.Fatal("零 TTL 应返回回源结果", err)
			}
			wantTTL(t, cache, "load", TTLNotFound, false)
		}
		if loads != 2 {
			t.Fatal("零 TTL 回源不应留下缓存")
		}
	})
}

func TestFileTTLStatesSurviveRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cache.json")
	cache, err := openTestFileCache(t, path)
	if err != nil {
		t.Fatal(err)
	}
	mustSet(t, cache, "permanent", "saved", NoExpiration)
	mustSet(t, cache, "deleted", "old", NoExpiration)
	mustSet(t, cache, "deleted", "immediate", 0)
	err = cache.Close()
	if err != nil {
		t.Fatal(err)
	}
	reopened, err := openTestFileCache(t, path)
	if err != nil {
		t.Fatal(err)
	}
	wantValue(t, reopened, "permanent", "saved", true)
	wantTTL(t, reopened, "permanent", NoExpiration, true)
	wantTTL(t, reopened, "deleted", TTLNotFound, false)
}

func TestRedisStoreHonorsClientClose(t *testing.T) {
	cache := newTestCache(t, RedisStore, "client_lifecycle", miniredis.RunT(t))
	client := cache.store.(*redisStore).client
	err := client.Close()
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = cache.Get(t.Context(), "key")
	if !errors.Is(err, tkredis.ErrClosed) {
		t.Fatal("Redis Store 必须经过 tkredis 客户端，不能绕过关闭状态", err)
	}
}
