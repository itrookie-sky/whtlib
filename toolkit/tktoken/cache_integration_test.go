package tktoken

import (
	"crypto/rand"
	"errors"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/itrookie-sky/whtlib/toolkit/tkcache"
	"github.com/itrookie-sky/whtlib/toolkit/tkredis"
	"github.com/redis/go-redis/v9"
)

var _ Store = (*tkcache.Cache)(nil)

func TestCacheStoreIntegration(t *testing.T) {
	for _, backend := range []tkcache.Backend{tkcache.BackendMemory, tkcache.BackendRedis} {
		t.Run(string(backend), func(t *testing.T) {
			config := tkcache.Config{Backend: backend, Namespace: "token_integration"}
			if backend == tkcache.BackendRedis {
				server := miniredis.RunT(t)
				client, err := tkredis.New(&redis.Options{Addr: server.Addr(), MaxRetries: -1})
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = client.Close() })
				config.RedisClient = client
			}
			cache, err := tkcache.New(config)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = cache.Close() })
			key := make([]byte, 16)
			_, err = rand.Read(key)
			if err != nil {
				t.Fatal(err)
			}
			manager, err := NewLegacy(Config{EncryptKey: key, Timeout: 10000, MaxRefresh: 1000}, cache)
			if err != nil {
				t.Fatal(err)
			}
			now := time.Now()
			manager.now = func() (value time.Time) { return now }
			old, err := manager.GenToken(t.Context(), "user", map[string]int64{"id": 9007199254740993})
			if err != nil {
				t.Fatal(err)
			}
			fresh, err := manager.GenToken(t.Context(), "user", "new session")
			if err != nil || fresh.Token == old.Token {
				t.Fatalf("重新登录应替换会话: %v", err)
			}
			now = now.Add(2 * time.Second)
			_, err = manager.ValidToken(t.Context(), old.Token)
			if !errors.Is(err, ErrUnauthorized) {
				t.Fatalf("旧 token 应失效: %v", err)
			}
			err = manager.RemoveToken(t.Context(), old.Token)
			if !errors.Is(err, ErrUnauthorized) {
				t.Fatalf("旧 token 不应撤销新会话: %v", err)
			}
			session, err := manager.ValidToken(t.Context(), fresh.Token)
			if err != nil || session.CreateTime != fresh.Session.CreateTime || session.LastRefreshTime != now.UnixMilli() {
				t.Fatalf("新会话原子续期失败: %+v,%v", session, err)
			}
			err = manager.RemoveToken(t.Context(), fresh.Token)
			if err != nil {
				t.Fatal(err)
			}
			_, err = manager.ValidToken(t.Context(), fresh.Token)
			if !errors.Is(err, ErrNotFound) {
				t.Fatalf("撤销后会话应缺失: %v", err)
			}
		})
	}
}
