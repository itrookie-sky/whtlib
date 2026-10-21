package tkcache

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

type redisStore struct{ client redis.UniversalClient }

// 单 key 脚本使比较、读取和 TTL 更新在一个原子操作中完成。
var mutationScript = redis.NewScript(`
local op=ARGV[1]
local ttl=tonumber(ARGV[2])
local v=redis.call('GET',KEYS[1])
local function put(value)
 if ttl>0 then redis.call('SET',KEYS[1],value,'PX',ttl) else redis.call('SET',KEYS[1],value) end
end
if op=='set' then put(ARGV[3]); return {1} end
if op=='nx' then if not v then put(ARGV[3]); return {1} else return {0} end end
if op=='getset' then if v then return {0,v} else put(ARGV[3]); return {1,ARGV[3]} end end
if op=='getdel' then if v then redis.call('DEL',KEYS[1]); return {1,v} else return {0} end end
if op=='getexpire' then
 if not v then return {0} end
 if ttl>0 then redis.call('PEXPIRE',KEYS[1],ttl) else redis.call('PERSIST',KEYS[1]) end
 return {1,v}
end
if op=='cas' then if v and v==ARGV[4] then put(ARGV[3]); return {1} else return {0} end end
if op=='cad' then if v and v==ARGV[4] then redis.call('DEL',KEYS[1]); return {1} else return {0} end end
return redis.error_reply('invalid cache operation')
`)

func (r *redisStore) get(ctx context.Context, key string) (value []byte, found bool, err error) {
	value, err = r.client.Get(ctx, key).Bytes()
	if errors.Is(err, redis.Nil) {
		return nil, false, nil
	}
	return value, err == nil, err
}

func (r *redisStore) apply(ctx context.Context, key string, req request) (result outcome, err error) {
	value, err := mutationScript.Run(ctx, r.client, []string{key}, string(req.operation), int64(req.ttl/time.Millisecond), copyValue(req.value), copyValue(req.expected)).Result()
	if err != nil {
		return
	}
	values, ok := value.([]any)
	if !ok || len(values) == 0 {
		return result, fmt.Errorf("tkcache: unexpected script result %T", value)
	}
	flag, ok := values[0].(int64)
	if !ok {
		return result, fmt.Errorf("tkcache: unexpected script flag %T", values[0])
	}
	result.changed = flag == 1
	if req.operation == opGetSet || ((req.operation == opGetDelete || req.operation == opGetExpire) && flag == 1) {
		if len(values) != 2 {
			return result, fmt.Errorf("tkcache: missing script value")
		}
		text, ok := values[1].(string)
		if !ok {
			return result, fmt.Errorf("tkcache: unexpected script value %T", values[1])
		}
		result.value = []byte(text)
		result.found = true
	}
	return
}

func (r *redisStore) ttl(ctx context.Context, key string) (ttl time.Duration, found bool, err error) {
	ttl, err = r.client.PTTL(ctx, key).Result()
	if err != nil {
		return
	}
	// go-redis 对无到期时间与不存在使用原始负数哨兵，不是负毫秒。
	if ttl == -2 {
		return 0, false, nil
	}
	if ttl == -1 {
		return 0, true, nil
	}
	return max(ttl, 0), true, nil
}

func (r *redisStore) clearNode(ctx context.Context, client redis.UniversalClient, prefix string) (err error) {
	var cursor uint64
	for {
		err = ctx.Err()
		if err != nil {
			return
		}
		var keys []string
		keys, cursor, err = client.Scan(ctx, cursor, prefix+"*", 128).Result()
		if err != nil {
			return
		}
		if len(keys) > 0 {
			pipeline := client.Pipeline()
			for _, key := range keys {
				pipeline.Del(ctx, key)
			}
			_, err = pipeline.Exec(ctx)
			if err != nil {
				return
			}
		}
		if cursor == 0 {
			return
		}
	}
}

func (r *redisStore) clear(ctx context.Context, prefix string) (err error) {
	if cluster, ok := r.client.(*redis.ClusterClient); ok {
		return cluster.ForEachMaster(ctx, func(ctx context.Context, client *redis.Client) (err error) { return r.clearNode(ctx, client, prefix) })
	}
	return r.clearNode(ctx, r.client, prefix)
}

func (r *redisStore) close() (err error) { return }
