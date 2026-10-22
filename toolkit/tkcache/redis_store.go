package tkcache

import (
	"context"
	"time"

	"github.com/itrookie-sky/whtlib/toolkit/tkredis"
)

// redisStore 只适配缓存接口，Redis 命令、TTL 和原子操作由 tkredis 提供。
type redisStore struct{ client *tkredis.Client }

func (r *redisStore) get(ctx context.Context, key string) (value []byte, found bool, err error) {
	return r.client.Get(ctx, key)
}

func (r *redisStore) apply(ctx context.Context, key string, req request) (result outcome, err error) {
	switch req.operation {
	case opSet:
		err = r.client.Set(ctx, key, req.value, req.ttl)
		result.changed = err == nil
	case opSetNX:
		result.changed, err = r.client.SetIfAbsent(ctx, key, req.value, req.ttl)
	case opGetSet:
		result.value, result.changed, err = r.client.GetOrSet(ctx, key, req.value, req.ttl)
		result.found = err == nil
	case opGetDelete:
		result.value, result.found, err = r.client.GetAndDelete(ctx, key)
		result.changed = result.found
	case opGetExpire:
		result.value, result.found, err = r.client.GetAndExpire(ctx, key, req.ttl)
		result.changed = result.found
	case opCAS:
		result.changed, err = r.client.CompareAndSwap(ctx, key, req.expected, req.value, req.ttl)
	case opCAD:
		result.changed, err = r.client.Cad(ctx, key, req.expected)
	default:
		err = ErrInvalidArgument
	}
	return
}

func (r *redisStore) ttl(ctx context.Context, key string) (ttl time.Duration, found bool, err error) {
	ttl, err = r.client.PTTL(ctx, key)
	return ttl, err == nil && ttl != TTLNotFound, err
}

func (r *redisStore) clear(ctx context.Context, prefix string) (err error) {
	return r.client.DeletePrefix(ctx, prefix)
}

func (r *redisStore) close() (err error) { return }
