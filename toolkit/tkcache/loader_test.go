package tkcache

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type loadResult struct {
	value []byte
	err   error
}

// 等待者进入 select 时才调用 Done，用于确定已加入共享回源。
type observedContext struct {
	context.Context
	once    sync.Once
	waiting chan struct{}
}

// 后端使用原始 ctx，避免 Redis 连接池调用 Done 被误判为已加入回源。
type unobservedStore struct{ store }

func (s unobservedStore) get(ctx context.Context, key string) (value []byte, found bool, err error) {
	if observed, ok := ctx.(*observedContext); ok {
		ctx = observed.Context
	}
	return s.store.get(ctx, key)
}

func (c *observedContext) Done() (done <-chan struct{}) {
	c.once.Do(func() { close(c.waiting) })
	return c.Context.Done()
}

func awaitSignal(t *testing.T, signal <-chan struct{}) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(3 * time.Second):
		t.Fatal("等待回源状态超时")
	}
}

func awaitResult(t *testing.T, result <-chan loadResult) (value loadResult) {
	t.Helper()
	select {
	case value = <-result:
	case <-time.After(3 * time.Second):
		t.Fatal("等待回源结果超时")
	}
	return
}

func startLoad(cache *Cache, ctx context.Context, key string, loader Loader) (result <-chan loadResult) {
	output := make(chan loadResult, 1)
	go func() {
		value, err := cache.GetOrLoad(ctx, key, time.Minute, loader)
		output <- loadResult{value, err}
	}()
	return output
}

func TestSingleFlightAndCopies(t *testing.T) {
	eachBackend(t, func(t *testing.T, cache *Cache, _ func(time.Duration)) {
		cache.backend = unobservedStore{cache.backend}
		started, release := make(chan struct{}), make(chan struct{})
		var calls atomic.Int32
		loader := func(ctx context.Context) (value []byte, err error) {
			calls.Add(1)
			close(started)
			select {
			case <-release:
			case <-ctx.Done():
				return nil, ctx.Err()
			}
			// 回源期间允许操作另一个 key，避免回调执行时持有数据锁。
			err = cache.SetString(ctx, "other", "available", 0)
			return []byte("shared"), err
		}
		leader := startLoad(cache, t.Context(), "key", loader)
		awaitSignal(t, started)
		followers := make([]<-chan loadResult, 24)
		for index := range followers {
			ctx := &observedContext{Context: t.Context(), waiting: make(chan struct{})}
			followers[index] = startLoad(cache, ctx, "key", loader)
			awaitSignal(t, ctx.waiting)
		}
		close(release)
		first := awaitResult(t, leader)
		if first.err != nil || string(first.value) != "shared" {
			t.Fatalf("共享回源失败: %+v", first)
		}
		first.value[0] = 'X'
		for _, follower := range followers {
			result := awaitResult(t, follower)
			if result.err != nil || string(result.value) != "shared" {
				t.Fatalf("等待者应得到独立结果: %+v", result)
			}
		}
		if calls.Load() != 1 {
			t.Fatalf("同 key 应仅回源一次: %d", calls.Load())
		}
		wantValue(t, cache, "key", "shared", true)
		wantValue(t, cache, "other", "available", true)
	})
}

func TestWaiterAndLeaderCancellation(t *testing.T) {
	eachBackend(t, func(t *testing.T, cache *Cache, _ func(time.Duration)) {
		cache.backend = unobservedStore{cache.backend}
		started, release := make(chan struct{}), make(chan struct{})
		leaderCtx, cancelLeader := context.WithCancel(t.Context())
		defer cancelLeader()
		leader := startLoad(cache, leaderCtx, "key", func(ctx context.Context) (value []byte, err error) {
			close(started)
			select {
			case <-release:
				return []byte("value"), nil
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		})
		awaitSignal(t, started)
		waiterCtx, cancelWaiter := context.WithCancel(t.Context())
		observed := &observedContext{Context: waiterCtx, waiting: make(chan struct{})}
		waiter := startLoad(cache, observed, "key", func(context.Context) (value []byte, err error) {
			return nil, errors.New("不应运行等待者的 loader")
		})
		awaitSignal(t, observed.waiting)
		cancelWaiter()
		result := awaitResult(t, waiter)
		if !errors.Is(result.err, context.Canceled) {
			t.Fatalf("等待者应能独立取消: %v", result.err)
		}
		select {
		case result = <-leader:
			t.Fatalf("等待者取消不应结束回源: %+v", result)
		default:
		}
		observed = &observedContext{Context: t.Context(), waiting: make(chan struct{})}
		follower := startLoad(cache, observed, "key", nil)
		// nil loader 属于参数错误，不加入共享任务。
		result = awaitResult(t, follower)
		if !errors.Is(result.err, ErrInvalidArgument) {
			t.Fatalf("nil loader: %v", result.err)
		}
		follower = startLoad(cache, observed, "key", func(context.Context) (value []byte, err error) { return })
		awaitSignal(t, observed.waiting)
		cancelLeader()
		for _, pending := range []<-chan loadResult{leader, follower} {
			result = awaitResult(t, pending)
			if !errors.Is(result.err, context.Canceled) {
				t.Fatalf("发起者取消应通知本轮所有等待者: %v", result.err)
			}
		}
		result = awaitResult(t, startLoad(cache, t.Context(), "key", func(context.Context) (value []byte, err error) {
			return []byte("retry"), nil
		}))
		if result.err != nil || string(result.value) != "retry" {
			t.Fatalf("取消后应允许重新回源: %+v", result)
		}
	})
}

func TestMutationsPreventStaleWriteback(t *testing.T) {
	for _, mutation := range []string{"set", "delete", "clear"} {
		t.Run(mutation, func(t *testing.T) {
			eachBackend(t, func(t *testing.T, cache *Cache, _ func(time.Duration)) {
				started, release := make(chan struct{}), make(chan struct{})
				old := startLoad(cache, t.Context(), "key", func(context.Context) (value []byte, err error) {
					close(started)
					<-release
					return []byte("old"), nil
				})
				awaitSignal(t, started)
				var err error
				switch mutation {
				case "set":
					err = cache.SetString(t.Context(), "key", "new", 0)
				case "delete":
					err = cache.Delete(t.Context(), "key")
				case "clear":
					err = cache.Clear(t.Context())
				}
				if err != nil {
					t.Fatal(err)
				}
				fresh := startLoad(cache, t.Context(), "key", func(context.Context) (value []byte, err error) {
					return []byte("new"), nil
				})
				result := awaitResult(t, fresh)
				if result.err != nil || string(result.value) != "new" {
					t.Fatalf("后续调用不能再等待旧回源: %+v", result)
				}
				close(release)
				result = awaitResult(t, old)
				if result.err != nil || string(result.value) != "old" {
					t.Fatalf("旧等待者可取得本轮快照: %+v", result)
				}
				wantValue(t, cache, "key", "new", true)
			})
		})
	}
}

func TestLoaderErrorsAndClose(t *testing.T) {
	eachBackend(t, func(t *testing.T, cache *Cache, _ func(time.Duration)) {
		problem := errors.New("database failed")
		_, err := cache.GetOrLoad(t.Context(), "error", 0, func(context.Context) (value []byte, err error) { return nil, problem })
		if !errors.Is(err, problem) {
			t.Fatal(err)
		}
		_, err = cache.GetOrLoad(t.Context(), "panic", 0, func(context.Context) (value []byte, err error) { panic("bad loader") })
		if !errors.Is(err, ErrLoaderPanic) {
			t.Fatalf("回源 panic 应唤醒等待者: %v", err)
		}
		wantValue(t, cache, "error", "", false)
		wantValue(t, cache, "panic", "", false)
		started, release := make(chan struct{}), make(chan struct{})
		defer close(release)
		pending := startLoad(cache, t.Context(), "blocked", func(context.Context) (value []byte, err error) {
			close(started)
			<-release
			return []byte("too late"), nil
		})
		awaitSignal(t, started)
		// 删除会让任务离开可加入索引，但 Close 仍必须找到并唤醒它。
		err = cache.Delete(t.Context(), "blocked")
		if err != nil {
			t.Fatal(err)
		}
		err = cache.Close()
		if err != nil {
			t.Fatal(err)
		}
		result := awaitResult(t, pending)
		if !errors.Is(result.err, ErrClosed) {
			t.Fatalf("Close 应唤醒忽略 ctx 的任务等待者: %v", result.err)
		}
	})
}

func TestJSONWaiterUsesInitiatorEmptyPolicy(t *testing.T) {
	eachBackend(t, func(t *testing.T, cache *Cache, _ func(time.Duration)) {
		cache.backend = unobservedStore{cache.backend}
		started, release := make(chan struct{}), make(chan struct{})
		leader := make(chan error, 1)
		go func() {
			_, err := GetOrLoadJSON(t.Context(), cache, "empty-policy", 0, func(context.Context) (value *int, err error) {
				close(started)
				<-release
				return
			}, WithCacheEmpty(false))
			leader <- err
		}()
		awaitSignal(t, started)
		ctx := &observedContext{Context: t.Context(), waiting: make(chan struct{})}
		follower := make(chan error, 1)
		go func() {
			value, err := GetOrLoadJSON(ctx, cache, "empty-policy", 0, func(context.Context) (value *int, err error) {
				return nil, errors.New("不应另行回源")
			}, WithCacheEmpty(true))
			if value != nil {
				err = errors.New("空回源应返回 nil")
			}
			follower <- err
		}()
		awaitSignal(t, ctx.waiting)
		close(release)
		for _, result := range []<-chan error{leader, follower} {
			select {
			case err := <-result:
				if err != nil {
					t.Fatal(err)
				}
			case <-time.After(3 * time.Second):
				t.Fatal("等待 JSON 回源结果超时")
			}
		}
		wantValue(t, cache, "empty-policy", "", false)
	})
}

func TestBackendFailureDoesNotLoad(t *testing.T) {
	eachBackend(t, func(t *testing.T, cache *Cache, _ func(time.Duration)) {
		backend, redisBackend := cache.backend.(*redisStore)
		if !redisBackend {
			return
		}
		key, _ := cache.dataKey("wrongtype")
		err := backend.client.LPush(t.Context(), key, "list").Err()
		if err != nil {
			t.Fatal(err)
		}
		called := false
		_, err = cache.GetOrLoad(t.Context(), "wrongtype", 0, func(context.Context) (value []byte, err error) {
			called = true
			return []byte("fallback"), nil
		})
		if err == nil || called {
			t.Fatalf("后端故障应直接返回: called=%v,err=%v", called, err)
		}
	})
}
