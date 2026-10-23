package tkcache

import (
	"bytes"
	"context"
	"time"
)

type flight struct {
	key      string
	ctx      context.Context
	cancel   context.CancelFunc
	stop     func() bool
	done     chan struct{}
	value    []byte
	err      error
	finished bool
}

// GetOrLoad 合并同一实例同 key 的回源；等待者可以独立取消。
func (c *Cache) GetOrLoad(ctx context.Context, key string, ttlDuration time.Duration, loader Loader, options ...LoadOption) (value []byte, err error) {
	err = c.check(ctx)
	if err != nil {
		return
	}
	if loader == nil {
		return nil, ErrInvalidArgument
	}
	ttl, err := normalizeTTL(ttlDuration)
	if err != nil {
		return
	}
	value, found, err := c.Get(ctx, key)
	if err != nil || found {
		return value, err
	}
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil, ErrClosed
	}
	current, exists := c.flights[key]
	if !exists {
		sharedCtx, cancel := context.WithCancel(ctx)
		current = &flight{key: key, ctx: sharedCtx, cancel: cancel, done: make(chan struct{})}
		c.flights[key] = current
		c.active[current] = struct{}{}
		current.stop = context.AfterFunc(sharedCtx, func() { c.finish(current, nil, sharedCtx.Err()) })
	}
	c.mu.Unlock()
	if !exists {
		go c.runLoad(current, ttl, loader, buildLoadOptions(options))
	}
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-current.done:
		return bytes.Clone(current.value), current.err
	}
}

func (c *Cache) finish(f *flight, value []byte, err error) {
	c.mu.Lock()
	if f.finished {
		c.mu.Unlock()
		return
	}
	// 取消可能先于 AfterFunc 的调度，交付时仍需检查共享上下文。
	contextErr := f.ctx.Err()
	if contextErr != nil {
		value, err = nil, contextErr
	}
	f.finished = true
	f.value = bytes.Clone(value)
	f.err = err
	if c.flights[f.key] == f {
		delete(c.flights, f.key)
	}
	delete(c.active, f)
	close(f.done)
	c.mu.Unlock()
	if f.stop != nil {
		f.stop()
	}
	f.cancel()
}

func (c *Cache) runLoad(f *flight, ttl time.Duration, loader Loader, options loadOptions) {
	defer func() {
		recovered := recover()
		if recovered != nil {
			c.finish(f, nil, loaderPanic(recovered))
		}
	}()
	value, found, err := c.Get(f.ctx, f.key)
	if err != nil || found {
		c.finish(f, value, err)
		return
	}
	value, err = loader(f.ctx)
	if err != nil {
		c.finish(f, nil, err)
		return
	}
	if value == nil && !options.cacheEmpty {
		c.finish(f, nil, nil)
		return
	}
	physical, _ := c.dataKey(f.key)
	c.gate.RLock()
	lock := c.stripe(physical)
	lock.Lock()
	c.mu.Lock()
	allowed := !c.closed && !f.finished && c.flights[f.key] == f
	c.mu.Unlock()
	if allowed {
		err = f.ctx.Err()
		if err == nil {
			_, err = c.store.apply(f.ctx, physical, request{operation: opSet, value: bytes.Clone(value), ttl: ttl})
		}
	}
	lock.Unlock()
	c.gate.RUnlock()
	c.finish(f, value, err)
}
