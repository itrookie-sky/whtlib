package tkredis

import (
	"bytes"
	"context"
	"errors"
	"math"
	"time"

	"github.com/redis/go-redis/v9"
)

const (
	// NoExpiration 是永久保存的原始 TTL 哨兵，不是负秒或负毫秒。
	NoExpiration time.Duration = -1
	// TTLNotFound 是 PTTL 查询中键不存在的原始哨兵，不能用于写入。
	TTLNotFound time.Duration = -2
)

// NormalizeTTL 保留 -1 和 0，正数向上取整到毫秒，其他负数返回错误。
func NormalizeTTL(ttl time.Duration) (normalized time.Duration, err error) {
	if ttl < NoExpiration {
		return 0, ErrInvalidTTL
	}
	if ttl <= 0 {
		return ttl, nil
	}
	milliseconds := int64(ttl / time.Millisecond)
	if ttl%time.Millisecond != 0 {
		milliseconds++
	}
	if milliseconds > math.MaxInt64/int64(time.Millisecond) {
		return 0, ErrInvalidTTL
	}
	return time.Duration(milliseconds) * time.Millisecond, nil
}

func ttlMilliseconds(ttl time.Duration) (milliseconds int64, err error) {
	normalized, err := NormalizeTTL(ttl)
	if err != nil {
		return
	}
	if normalized <= 0 {
		return int64(normalized), nil
	}
	return int64(normalized / time.Millisecond), nil
}

// Get 读取字符串原始字节，区分空值和不存在，保留非未命中错误。
func (c *Client) Get(ctx context.Context, key string) (value []byte, found bool, err error) {
	raw, err := c.Do(ctx, "GET", key)
	if errors.Is(err, redis.Nil) {
		return nil, false, nil
	}
	if err != nil {
		return
	}
	value, err = stringBytes(raw)
	return value, err == nil, err
}

// Set 覆盖字符串原始字节；-1 永久，0 立即删除，正数设置期限。
func (c *Client) Set(ctx context.Context, key string, value []byte, ttl time.Duration) (err error) {
	milliseconds, err := ttlMilliseconds(ttl)
	if err != nil {
		return
	}
	if milliseconds == 0 {
		_, err = c.Do(ctx, "DEL", key)
		return
	}
	args := []any{key, bytes.Clone(value)}
	if milliseconds > 0 {
		args = append(args, "PX", milliseconds)
	}
	_, err = c.Do(ctx, "SET", args...)
	return
}

// SetIfAbsent 原子写入不存在的键；0 TTL 条件成功后不保留键。
func (c *Client) SetIfAbsent(ctx context.Context, key string, value []byte, ttl time.Duration) (stored bool, err error) {
	result, err := c.mutateString(ctx, "nx", key, value, nil, ttl)
	return result.changed, err
}

// GetOrSet 原子返回已有字节或写入候选值，命中不续期；stored 表示写入条件成功。
// TTL 为 0 且未命中时返回候选值和 stored=true，但不留下键。
func (c *Client) GetOrSet(ctx context.Context, key string, value []byte, ttl time.Duration) (actual []byte, stored bool, err error) {
	result, err := c.mutateString(ctx, "getset", key, value, nil, ttl)
	return result.value, result.changed, err
}

// GetAndExpire 原子读取并设置期限；-1 移除到期时间，0 返回旧值后删除。
func (c *Client) GetAndExpire(ctx context.Context, key string, ttl time.Duration) (value []byte, found bool, err error) {
	result, err := c.mutateString(ctx, "getexpire", key, nil, nil, ttl)
	return result.value, result.found, err
}

// GetAndDelete 原子读取并删除字符串，未命中返回 found=false。
func (c *Client) GetAndDelete(ctx context.Context, key string) (value []byte, found bool, err error) {
	result, err := c.mutateString(ctx, "getdel", key, nil, nil, NoExpiration)
	return result.value, result.found, err
}

// CompareAndSwap 仅在字节匹配时原子更新内容和期限，不能创建缺失的键。
func (c *Client) CompareAndSwap(ctx context.Context, key string, expected, value []byte, ttl time.Duration) (swapped bool, err error) {
	result, err := c.mutateString(ctx, "cas", key, value, expected, ttl)
	return result.changed, err
}

// PTTL 返回毫秒精度的剩余期限；-1 永久，-2 不存在，哨兵不缩放。
func (c *Client) PTTL(ctx context.Context, key string) (ttl time.Duration, err error) {
	raw, err := c.Do(ctx, "PTTL", key)
	if err != nil {
		return
	}
	milliseconds, ok := raw.(int64)
	if !ok {
		return 0, unexpectedResponse("unexpected PTTL response")
	}
	if milliseconds == -1 || milliseconds == -2 {
		return time.Duration(milliseconds), nil
	}
	if milliseconds < 0 || milliseconds > math.MaxInt64/int64(time.Millisecond) {
		return 0, ErrInvalidTTL
	}
	return time.Duration(milliseconds) * time.Millisecond, nil
}

type stringMutationResult struct {
	value   []byte
	found   bool
	changed bool
}

// 单 key Lua 保留读取、条件判断与 TTL 修改的原子性，复用统一脚本执行器。
var stringMutationScript = redis.NewScript(`
local op=ARGV[1]
local ttl=tonumber(ARGV[2])
local function put(value)
 if ttl==0 then redis.call('DEL',KEYS[1])
 elseif ttl==-1 then redis.call('SET',KEYS[1],value)
 else redis.call('SET',KEYS[1],value,'PX',ttl) end
end
if op=='nx' then
 if redis.call('EXISTS',KEYS[1])==0 then put(ARGV[3]); return {1} end
 return {0}
end
local v=redis.call('GET',KEYS[1])
if op=='getset' then if v then return {0,v} else put(ARGV[3]); return {1,ARGV[3]} end end
if op=='getdel' then if v then redis.call('DEL',KEYS[1]); return {1,v} else return {0} end end
if op=='getexpire' then
 if not v then return {0} end
 if ttl==-1 then redis.call('PERSIST',KEYS[1]) else redis.call('PEXPIRE',KEYS[1],ttl) end
 return {1,v}
end
if op=='cas' then if v and v==ARGV[4] then put(ARGV[3]); return {1} else return {0} end end
return redis.error_reply('invalid string operation')
`)

func (c *Client) mutateString(ctx context.Context, operation, key string, value, expected []byte, ttl time.Duration) (result stringMutationResult, err error) {
	milliseconds, err := ttlMilliseconds(ttl)
	if err != nil {
		return
	}
	raw, err := c.runScript(ctx, stringMutationScript, []string{key}, operation, milliseconds, bytes.Clone(value), bytes.Clone(expected))
	if err != nil {
		return
	}
	values, ok := raw.([]any)
	if !ok || len(values) == 0 {
		return result, unexpectedResponse("unexpected string mutation response")
	}
	flag, ok := values[0].(int64)
	if !ok || (flag != 0 && flag != 1) {
		return result, unexpectedResponse("unexpected string mutation flag")
	}
	result.changed = flag == 1
	if operation == "getset" || ((operation == "getdel" || operation == "getexpire") && result.changed) {
		if len(values) != 2 {
			return result, unexpectedResponse("missing string mutation value")
		}
		result.value, err = stringBytes(values[1])
		result.found = err == nil
	}
	return
}

func stringBytes(raw any) (value []byte, err error) {
	switch data := raw.(type) {
	case string:
		return []byte(data), nil
	case []byte:
		return bytes.Clone(data), nil
	}
	return nil, unexpectedResponse("unexpected string response")
}
