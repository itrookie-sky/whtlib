package tkredis

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
)

// LockOption 设置分布式锁选项。
type LockOption func(lock *Lock)

// WithExpiration 设置租约时长，默认十秒。
func WithExpiration(expiration time.Duration) (option LockOption) {
	return func(lock *Lock) { lock.expiration = expiration }
}

// WithRetryInterval 设置自旋重试间隔，默认十毫秒。
func WithRetryInterval(interval time.Duration) (option LockOption) {
	return func(lock *Lock) { lock.retryInterval = interval }
}

// Lock 同一实例同时只持有一份租约，可在释放后重新获取。
type Lock struct {
	client        *Client
	key           string
	expiration    time.Duration
	retryInterval time.Duration
	mu            sync.Mutex
	lease         *lockLease
}

type lockLease struct {
	owner    string
	cancel   context.CancelFunc
	done     chan struct{}
	released bool
	err      error
}

// NewLock 创建锁对象，不访问服务器。
func (c *Client) NewLock(key string, options ...LockOption) (lock *Lock, err error) {
	err = c.checkOpen()
	if err != nil {
		return
	}
	lock = &Lock{client: c, key: key, expiration: 10 * time.Second, retryInterval: 10 * time.Millisecond}
	for _, option := range options {
		if option != nil {
			option(lock)
		}
	}
	if key == "" || lock.expiration < time.Millisecond || lock.retryInterval <= 0 {
		lock = nil
		err = invalidArgument("lock requires a key, expiration >= 1ms and a positive retry interval")
	}
	return
}

// NewRedisLock 提供旧构造方法名的显式迁移入口，返回新的 Lock 类型。
func (c *Client) NewRedisLock(key string, options ...LockOption) (lock *Lock, err error) {
	return c.NewLock(key, options...)
}

// TryLock 以 SET NX PX 获取租约，并启动持有者校验续期。
// ctx 取消或客户端关闭后停止续期，并尝试原子释放自己的租约。
func (l *Lock) TryLock(ctx context.Context) (ok bool, err error) {
	ctx = nonNilContext(ctx)
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.lease != nil {
		err = ErrLockHeld
		return
	}
	err = l.client.beginTask()
	if err != nil {
		return
	}
	started := false
	defer func() {
		if !started {
			l.client.wg.Done()
		}
	}()
	leaseCtx, cancel := context.WithCancel(ctx)
	stopClientCancel := context.AfterFunc(l.client.ctx, cancel)
	defer func() {
		if !started {
			stopClientCancel()
			cancel()
		}
	}()
	err = leaseCtx.Err()
	if err != nil {
		return
	}
	random := make([]byte, 24)
	_, err = rand.Read(random)
	if err != nil {
		return
	}
	owner := hex.EncodeToString(random)
	milliseconds, err := positiveMilliseconds(l.expiration)
	if err != nil {
		return
	}
	_, err = l.client.native.Do(leaseCtx, "SET", l.key, owner, "NX", "PX", milliseconds).Result()
	if errors.Is(err, redis.Nil) {
		err = nil
		return
	}
	if err != nil {
		return
	}
	lease := &lockLease{owner: owner, cancel: cancel, done: make(chan struct{})}
	l.lease = lease
	started = true
	ok = true
	go l.maintain(leaseCtx, lease, milliseconds, stopClientCancel)
	return
}

func (l *Lock) maintain(ctx context.Context, lease *lockLease, milliseconds int64, stopClientCancel func() bool) {
	defer l.client.wg.Done()
	defer stopClientCancel()
	defer lease.cancel()
	ticker := time.NewTicker(l.expiration / 3)
	defer ticker.Stop()
	defer func() {
		// 清理使用独立且有界的上下文，保证调用方取消后仍尝试释放自己的租约。
		cleanupCtx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		value, cleanupErr := l.client.native.Eval(cleanupCtx, compareDeleteScript, []string{l.key}, lease.owner).Int()
		lease.released = value == 1
		lease.err = errors.Join(lease.err, cleanupErr)
		l.mu.Lock()
		if l.lease == lease {
			l.lease = nil
		}
		close(lease.done)
		l.mu.Unlock()
	}()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			refreshCtx, cancel := context.WithTimeout(ctx, l.expiration/3)
			updated, err := l.client.native.Eval(refreshCtx, compareExpireScript, []string{l.key}, lease.owner, milliseconds).Int()
			cancel()
			if err != nil {
				if ctx.Err() != nil {
					return
				}
				lease.err = err
				l.client.warnMaintenance(ctx)
				return
			}
			if updated != 1 {
				lease.err = ErrLockLost
				l.client.warnMaintenance(ctx)
				return
			}
		}
	}
}

// SpinLock 最多尝试 attempts 次；等待响应 ctx 取消和客户端关闭。
func (l *Lock) SpinLock(ctx context.Context, attempts int) (ok bool, err error) {
	ctx = nonNilContext(ctx)
	if attempts <= 0 {
		err = invalidArgument("lock attempts must be positive")
		return
	}
	for i := range attempts {
		ok, err = l.TryLock(ctx)
		if err != nil || ok || i == attempts-1 {
			return
		}
		timer := time.NewTimer(l.retryInterval)
		select {
		case <-ctx.Done():
			timer.Stop()
			err = ctx.Err()
			return
		case <-l.client.ctx.Done():
			timer.Stop()
			err = ErrClosed
			return
		case <-timer.C:
		}
	}
	return
}

// Unlock 停止维护并等待原子比较删除；不会删除其他持有者的锁。
func (l *Lock) Unlock(ctx context.Context) (ok bool, err error) {
	l.mu.Lock()
	lease := l.lease
	if lease == nil {
		l.mu.Unlock()
		return
	}
	lease.cancel()
	l.mu.Unlock()
	select {
	case <-lease.done:
		ok, err = lease.released, lease.err
	case <-nonNilContext(ctx).Done():
		err = nonNilContext(ctx).Err()
	}
	return
}
