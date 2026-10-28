package tkredis

import (
	"time"
)

const commandExpireScript = `
local result = redis.call(ARGV[1], KEYS[1], unpack(ARGV, 3))
redis.call('PEXPIRE', KEYS[1], ARGV[2])
return result`

const compareDeleteScript = `
if redis.call('GET', KEYS[1]) == ARGV[1] then
  return redis.call('DEL', KEYS[1])
end
return 0`

const compareExpireScript = `
if redis.call('GET', KEYS[1]) == ARGV[1] then
  return redis.call('PEXPIRE', KEYS[1], ARGV[2])
end
return 0`

const (
	// NoExpiration 是永久保存的原始 TTL 哨兵，不是负秒或负毫秒。
	NoExpiration time.Duration = -1
	// TTLNotFound 是 PTTL 查询中键不存在的原始哨兵，不能用于写入。
	TTLNotFound time.Duration = -2
)
