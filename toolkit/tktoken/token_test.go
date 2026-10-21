package tktoken

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"
)

type memoryEntry struct {
	value   []byte
	expires time.Time
}

// memoryStore 仅供本包离线验证，不连接外部存储。
type memoryStore struct {
	mu              sync.Mutex
	entries         map[string]memoryEntry
	now             func() time.Time
	swaps           int
	successfulSwaps int
	deletes         int
}

func newMemoryStore() (store *memoryStore) {
	return &memoryStore{entries: make(map[string]memoryEntry), now: time.Now}
}

func (s *memoryStore) current(key string) (entry memoryEntry, found bool) {
	entry, found = s.entries[key]
	if found && !entry.expires.IsZero() && !entry.expires.After(s.now()) {
		delete(s.entries, key)
		found = false
	}
	return
}

func (s *memoryStore) Get(ctx context.Context, key string) (value []byte, found bool, err error) {
	err = ctx.Err()
	if err != nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	entry, found := s.current(key)
	return bytes.Clone(entry.value), found, nil
}

func (s *memoryStore) put(key string, value []byte, ttl time.Duration) {
	entry := memoryEntry{value: bytes.Clone(value)}
	if ttl > 0 {
		entry.expires = s.now().Add(ttl)
	}
	s.entries[key] = entry
}

func (s *memoryStore) Set(ctx context.Context, key string, value []byte, ttl time.Duration) (err error) {
	err = ctx.Err()
	if err != nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.put(key, value, ttl)
	return
}

func (s *memoryStore) SetIfAbsent(ctx context.Context, key string, value []byte, ttl time.Duration) (stored bool, err error) {
	err = ctx.Err()
	if err != nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	_, found := s.current(key)
	if found {
		return
	}
	s.put(key, value, ttl)
	stored = true
	return
}

func (s *memoryStore) CompareAndSwap(ctx context.Context, key string, expected, value []byte, ttl time.Duration) (swapped bool, err error) {
	err = ctx.Err()
	if err != nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.swaps++
	entry, found := s.current(key)
	if !found || !bytes.Equal(entry.value, expected) {
		return
	}
	s.put(key, value, ttl)
	s.successfulSwaps++
	swapped = true
	return
}

func (s *memoryStore) CompareAndDelete(ctx context.Context, key string, expected []byte) (deleted bool, err error) {
	err = ctx.Err()
	if err != nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.deletes++
	entry, found := s.current(key)
	if !found || !bytes.Equal(entry.value, expected) {
		return
	}
	delete(s.entries, key)
	deleted = true
	return
}

func testToken(t *testing.T, store Store, multiLogin bool) (token *Token) {
	t.Helper()
	var err error
	token, err = NewLegacy(Config{
		EncryptKey: []byte("0123456789abcdef"), Timeout: 1000, MaxRefresh: 500,
		MultiLogin: multiLogin,
	}, store)
	if err != nil {
		t.Fatal(err)
	}
	return
}

func TestTokenSessionLifecycle(t *testing.T) {
	ctx := t.Context()
	store := newMemoryStore()
	manager := testToken(t, store, false)
	now := time.Now().Truncate(time.Millisecond)
	manager.now = func() (value time.Time) { return now }
	store.now = manager.now
	first, err := manager.GenToken(ctx, "user", map[string]any{"id": json.Number("9007199254740993"), "nil": nil})
	if err != nil {
		t.Fatal(err)
	}
	now = now.Add(600 * time.Millisecond)
	refreshed, err := manager.ValidToken(ctx, first.Token)
	if err != nil {
		t.Fatal(err)
	}
	if refreshed.CreateTime != first.Session.CreateTime || refreshed.LastRefreshTime != now.UnixMilli() || refreshed.RefreshTime != now.Add(500*time.Millisecond).UnixMilli() {
		t.Fatal("续期必须保留创建时间，并更新独立的刷新时间")
	}
	var data map[string]any
	err = refreshed.DecodeData(&data)
	if err != nil {
		t.Fatal(err)
	}
	if data["id"] != json.Number("9007199254740993") || data["nil"] != nil {
		t.Fatal("业务数据的数字精度或空值发生变化")
	}
	now = now.Add(100 * time.Millisecond)
	second, err := manager.GenToken(ctx, "user", "replacement")
	if err != nil {
		t.Fatal(err)
	}
	if first.Token == second.Token {
		t.Fatal("单会话模式的新登录应替换旧身份")
	}
	before, _, err := store.Get(ctx, DefaultCacheKey+"user")
	if err != nil {
		t.Fatal(err)
	}
	now = now.Add(600 * time.Millisecond)
	_, err = manager.ValidToken(ctx, first.Token)
	if !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("旧令牌校验错误 = %v", err)
	}
	err = manager.RemoveToken(ctx, first.Token)
	if !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("旧令牌撤销错误 = %v", err)
	}
	after, found, err := store.Get(ctx, DefaultCacheKey+"user")
	if err != nil || !found || !bytes.Equal(before, after) {
		t.Fatal("旧令牌不能续期、覆盖或删除新登录会话")
	}
	err = manager.RemoveToken(ctx, second.Token)
	if err != nil {
		t.Fatal(err)
	}
	_, err = manager.ValidToken(ctx, second.Token)
	if !errors.Is(err, ErrUnauthorized) || !errors.Is(err, ErrNotFound) {
		t.Fatalf("已撤销令牌应同时匹配 unauthorized 和 notfound，实际 %v", err)
	}
}

func TestConcurrentMultiLoginReusesSession(t *testing.T) {
	store := newMemoryStore()
	manager := testToken(t, store, true)
	ctx := t.Context()
	const count = 24
	results := make(chan IssuedToken, count)
	errorsCh := make(chan error, count)
	var group sync.WaitGroup
	for index := range count {
		group.Go(func() {
			issued, err := manager.GenToken(ctx, "user", index)
			results <- issued
			errorsCh <- err
		})
	}
	group.Wait()
	close(results)
	close(errorsCh)
	for err := range errorsCh {
		if err != nil {
			t.Fatal(err)
		}
	}
	var first IssuedToken
	for result := range results {
		if first.Token == "" {
			first = result
		}
		if first.Token != result.Token || !bytes.Equal(first.Session.Data, result.Session.Data) {
			t.Fatal("并发多端登录必须复用相同令牌和原业务数据")
		}
	}
}

func TestConcurrentRefreshUsesCAS(t *testing.T) {
	ctx := t.Context()
	store := newMemoryStore()
	manager := testToken(t, store, false)
	now := time.Now().Truncate(time.Millisecond)
	manager.now = func() (value time.Time) { return now }
	store.now = manager.now
	issued, err := manager.GenToken(ctx, "user", nil)
	if err != nil {
		t.Fatal(err)
	}
	now = now.Add(600 * time.Millisecond)
	errorsCh := make(chan error, 24)
	var group sync.WaitGroup
	for range 24 {
		group.Go(func() {
			_, validErr := manager.ValidToken(ctx, issued.Token)
			errorsCh <- validErr
		})
	}
	group.Wait()
	close(errorsCh)
	for validErr := range errorsCh {
		if validErr != nil {
			t.Fatal(validErr)
		}
	}
	if store.successfulSwaps != 1 {
		t.Fatalf("同一阈值内成功续期次数 = %d", store.successfulSwaps)
	}
}

type hookedStore struct {
	*memoryStore
	beforeSwap   func()
	beforeDelete func()
}

func (s *hookedStore) CompareAndSwap(ctx context.Context, key string, expected, value []byte, ttl time.Duration) (swapped bool, err error) {
	if s.beforeSwap != nil {
		hook := s.beforeSwap
		s.beforeSwap = nil
		hook()
	}
	return s.memoryStore.CompareAndSwap(ctx, key, expected, value, ttl)
}

func (s *hookedStore) CompareAndDelete(ctx context.Context, key string, expected []byte) (deleted bool, err error) {
	if s.beforeDelete != nil {
		hook := s.beforeDelete
		s.beforeDelete = nil
		hook()
	}
	return s.memoryStore.CompareAndDelete(ctx, key, expected)
}

func TestConcurrentReplacementCannotBeRefreshedOrRemoved(t *testing.T) {
	for _, operation := range []string{"refresh", "remove"} {
		t.Run(operation, func(t *testing.T) {
			ctx := t.Context()
			store := &hookedStore{memoryStore: newMemoryStore()}
			manager := testToken(t, store, false)
			now := time.Now().Truncate(time.Millisecond)
			manager.now = func() (value time.Time) { return now }
			store.now = manager.now
			first, err := manager.GenToken(ctx, "user", "first")
			if err != nil {
				t.Fatal(err)
			}
			now = now.Add(600 * time.Millisecond)
			var replacement IssuedToken
			hook := func() {
				replacement, err = manager.GenToken(ctx, "user", "new")
				if err != nil {
					t.Fatal(err)
				}
			}
			if operation == "refresh" {
				store.beforeSwap = hook
				_, err = manager.ValidToken(ctx, first.Token)
			}
			if operation == "remove" {
				store.beforeDelete = hook
				err = manager.RemoveToken(ctx, first.Token)
			}
			if !errors.Is(err, ErrUnauthorized) {
				t.Fatalf("并发替换后旧操作应失败，实际 %v", err)
			}
			current, validErr := manager.ValidToken(ctx, replacement.Token)
			if validErr != nil || current.UUID != replacement.Session.UUID || current.CreateTime != replacement.Session.CreateTime {
				t.Fatal("旧操作破坏了替换会话")
			}
		})
	}
}

func TestConfigAndCanceledContext(t *testing.T) {
	store := newMemoryStore()
	codec, err := NewLegacyCodec([]byte("0123456789abcdef"), "_")
	if err != nil {
		t.Fatal(err)
	}
	for _, config := range []Config{
		{}, {Codec: codec, Timeout: -1}, {Codec: codec, Timeout: 1, MaxRefresh: 2},
		{Codec: codec, CacheMode: 4}, {Codec: codec, MaxRefresh: -1},
	} {
		_, err = New(config, store)
		if !errors.Is(err, ErrInvalidConfig) {
			t.Fatalf("错误配置未被拒绝: %v", err)
		}
	}
	_, err = NewLegacy(Config{}, store)
	if !errors.Is(err, ErrInvalidConfig) {
		t.Fatal("必须显式提供密钥")
	}
	_, err = New(Config{Codec: codec}, nil)
	if !errors.Is(err, ErrInvalidConfig) {
		t.Fatal("必须显式提供存储")
	}
	manager := testToken(t, store, false)
	issued, err := manager.GenToken(t.Context(), "user", nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err = manager.GenToken(ctx, "user", nil)
	if !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	_, err = manager.ValidToken(ctx, issued.Token)
	if !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	err = manager.RemoveToken(ctx, issued.Token)
	if !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}
