package tkredis

import (
	"context"
	"errors"
	"math"
	"time"
)

// ScoredMember 显式表示有序集合成员与分数；读取时 Member 是 Redis 原始字符串。
type ScoredMember struct {
	Member any
	Score  float64
}

const commandExpireScript = `
local result = redis.call(ARGV[1], KEYS[1], unpack(ARGV, 3))
redis.call('PEXPIRE', KEYS[1], ARGV[2])
return result`

func (c *Client) collectionCommand(ctx context.Context, command, key string, expiration []time.Duration, args ...any) (value any, err error) {
	ttl, err := collectionTTL(expiration)
	if err != nil {
		return
	}
	if ttl == 0 {
		commandArgs := make([]any, 0, len(args)+1)
		commandArgs = append(commandArgs, key)
		commandArgs = append(commandArgs, args...)
		return c.Do(ctx, command, commandArgs...)
	}
	scriptArgs := make([]any, 0, len(args)+2)
	scriptArgs = append(scriptArgs, command, ttl)
	scriptArgs = append(scriptArgs, args...)
	return c.Eval(ctx, commandExpireScript, []string{key}, scriptArgs...)
}

func collectionTTL(expiration []time.Duration) (milliseconds int64, err error) {
	if len(expiration) == 0 || expiration[0] == 0 {
		return
	}
	milliseconds, err = positiveMilliseconds(expiration[0])
	return
}

// SAdd 添加成员；正 TTL 在同一脚本内重置，零 TTL 保持原有到期时间。
func (c *Client) SAdd(ctx context.Context, key string, members []any, expiration ...time.Duration) (count int, err error) {
	if len(members) == 0 {
		return
	}
	value, err := c.collectionCommand(ctx, "SADD", key, expiration, members...)
	if err != nil {
		return
	}
	count, err = integerResult(value)
	return
}

// SIsMember 判断成员是否存在，可原子更新集合 TTL。
func (c *Client) SIsMember(ctx context.Context, key string, member any, expiration ...time.Duration) (exists bool, err error) {
	value, err := c.collectionCommand(ctx, "SISMEMBER", key, expiration, member)
	if err != nil {
		return
	}
	count, err := integerResult(value)
	exists = count == 1
	return
}

// SMembers 返回全部成员；缺失集合返回空切片。
func (c *Client) SMembers(ctx context.Context, key string, expiration ...time.Duration) (members []string, err error) {
	value, err := c.collectionCommand(ctx, "SMEMBERS", key, expiration)
	if err != nil {
		return
	}
	members, err = stringResults(value)
	return
}

// SPop 原子弹出至多 count 个成员，并更新剩余集合的 TTL。
func (c *Client) SPop(ctx context.Context, key string, count int, expiration ...time.Duration) (members []string, err error) {
	if count < 0 {
		err = errors.New("tkredis: set pop count must not be negative")
		return
	}
	value, err := c.collectionCommand(ctx, "SPOP", key, expiration, count)
	if err != nil {
		return
	}
	members, err = stringResults(value)
	return
}

// SUnion 返回并集；只有所有 key 存在时才在同一脚本内更新全部 TTL。
// Redis Cluster 要求所有 key 位于同一 slot。
func (c *Client) SUnion(ctx context.Context, keys []string, expiration ...time.Duration) (members []string, err error) {
	if len(keys) == 0 {
		members = []string{}
		return
	}
	ttl, err := collectionTTL(expiration)
	if err != nil {
		return
	}
	var value any
	if ttl == 0 {
		args := make([]any, len(keys))
		for i, key := range keys {
			args[i] = key
		}
		value, err = c.Do(ctx, "SUNION", args...)
	} else {
		value, err = c.Eval(ctx, `
local result = redis.call('SUNION', unpack(KEYS))
local allExist = true
for _, key in ipairs(KEYS) do
  if redis.call('EXISTS', key) == 0 then allExist = false end
end
if allExist then
  for _, key in ipairs(KEYS) do redis.call('PEXPIRE', key, ARGV[1]) end
end
return result`, keys, ttl)
	}
	if err != nil {
		return
	}
	members, err = stringResults(value)
	return
}

// SCard 返回集合成员数量，可原子更新 TTL。
func (c *Client) SCard(ctx context.Context, key string, expiration ...time.Duration) (count int, err error) {
	value, err := c.collectionCommand(ctx, "SCARD", key, expiration)
	if err != nil {
		return
	}
	count, err = integerResult(value)
	return
}

// SRem 删除指定成员，并可原子更新剩余集合的 TTL。
func (c *Client) SRem(ctx context.Context, key string, members []any, expiration ...time.Duration) (count int, err error) {
	if len(members) == 0 {
		return
	}
	value, err := c.collectionCommand(ctx, "SREM", key, expiration, members...)
	if err != nil {
		return
	}
	count, err = integerResult(value)
	return
}

// ZAdd 新增或更新成员，复杂成员可使用 JSON 包装。
func (c *Client) ZAdd(ctx context.Context, key string, members []ScoredMember, expiration ...time.Duration) (count int, err error) {
	if len(members) == 0 {
		return
	}
	args := make([]any, 0, len(members)*2)
	for _, member := range members {
		args = append(args, member.Score, member.Member)
	}
	value, err := c.collectionCommand(ctx, "ZADD", key, expiration, args...)
	if err != nil {
		return
	}
	count, err = integerResult(value)
	return
}

// ZRange 按排名返回成员，start/stop 使用 Redis 的含尾下标。
// descending 控制降序，withScores=false 时返回的 Score 为零。
func (c *Client) ZRange(ctx context.Context, key string, start, stop int, descending, withScores bool, expiration ...time.Duration) (members []ScoredMember, err error) {
	command := "ZRANGE"
	if descending {
		command = "ZREVRANGE"
	}
	args := []any{start, stop}
	if withScores {
		args = append(args, "WITHSCORES")
	}
	value, err := c.collectionCommand(ctx, command, key, expiration, args...)
	if err != nil {
		return
	}
	members, err = scoredResults(value, withScores)
	return
}

// ZPage 原子读取总数和指定页；页码从一开始。
func (c *Client) ZPage(ctx context.Context, key string, page, pageSize int, descending, withScores bool, expiration ...time.Duration) (total int, members []ScoredMember, err error) {
	if page <= 0 || pageSize <= 0 || page > math.MaxInt/pageSize {
		err = errors.New("tkredis: page and page size must be positive and within range")
		return
	}
	ttl, err := collectionTTL(expiration)
	if err != nil {
		return
	}
	command := "ZRANGE"
	if descending {
		command = "ZREVRANGE"
	}
	scores := ""
	if withScores {
		scores = "WITHSCORES"
	}
	value, err := c.Eval(ctx, `
local total = redis.call('ZCARD', KEYS[1])
local members
if ARGV[4] == 'WITHSCORES' then
  members = redis.call(ARGV[1], KEYS[1], ARGV[2], ARGV[3], 'WITHSCORES')
else
  members = redis.call(ARGV[1], KEYS[1], ARGV[2], ARGV[3])
end
if tonumber(ARGV[5]) > 0 then redis.call('PEXPIRE', KEYS[1], ARGV[5]) end
return {total, members}`, []string{key}, command, (page-1)*pageSize, page*pageSize-1, scores, ttl)
	if err != nil {
		return
	}
	parts, ok := value.([]any)
	if !ok || len(parts) != 2 {
		err = errors.New("tkredis: unexpected page script result")
		return
	}
	total, err = integerResult(parts[0])
	if err != nil {
		return
	}
	members, err = scoredResults(parts[1], withScores)
	return
}

// ZCard 返回有序集合成员数量。
func (c *Client) ZCard(ctx context.Context, key string, expiration ...time.Duration) (count int, err error) {
	value, err := c.collectionCommand(ctx, "ZCARD", key, expiration)
	if err != nil {
		return
	}
	count, err = integerResult(value)
	return
}

// ZCount 按 Redis 分数边界语法统计成员，例如 -inf、+inf 和 (10。
func (c *Client) ZCount(ctx context.Context, key, minimum, maximum string, expiration ...time.Duration) (count int, err error) {
	value, err := c.collectionCommand(ctx, "ZCOUNT", key, expiration, minimum, maximum)
	if err != nil {
		return
	}
	count, err = integerResult(value)
	return
}

// ZIncrBy 原子增加指定成员的分数。
func (c *Client) ZIncrBy(ctx context.Context, key string, increment float64, member any, expiration ...time.Duration) (score float64, err error) {
	value, err := c.collectionCommand(ctx, "ZINCRBY", key, expiration, increment, member)
	if err != nil {
		return
	}
	score, err = floatResult(value)
	return
}

// ZRangeByScore 按分数范围返回成员；始终传 minimum、maximum，降序时内部交换边界。
// limit 为空表示不分页，否则必须为非负的 [offset, count]。
func (c *Client) ZRangeByScore(ctx context.Context, key, minimum, maximum string, descending, withScores bool, limit []int, expiration ...time.Duration) (members []ScoredMember, err error) {
	if len(limit) != 0 && (len(limit) != 2 || limit[0] < 0 || limit[1] < 0) {
		err = errors.New("tkredis: score range limit must be [offset, count] with non-negative values")
		return
	}
	command := "ZRANGEBYSCORE"
	args := make([]any, 0, 6)
	if descending {
		command = "ZREVRANGEBYSCORE"
		args = append(args, maximum, minimum)
	} else {
		args = append(args, minimum, maximum)
	}
	if withScores {
		args = append(args, "WITHSCORES")
	}
	if len(limit) == 2 {
		args = append(args, "LIMIT", limit[0], limit[1])
	}
	value, err := c.collectionCommand(ctx, command, key, expiration, args...)
	if err != nil {
		return
	}
	members, err = scoredResults(value, withScores)
	return
}

// ZRank 返回零起始排名，成员不存在时保留 redis.Nil。
func (c *Client) ZRank(ctx context.Context, key string, member any, descending bool, expiration ...time.Duration) (rank int, err error) {
	command := "ZRANK"
	if descending {
		command = "ZREVRANK"
	}
	value, err := c.collectionCommand(ctx, command, key, expiration, member)
	if err != nil {
		return
	}
	rank, err = integerResult(value)
	return
}

// ZRem 删除有序集合中的成员。
func (c *Client) ZRem(ctx context.Context, key string, members []any, expiration ...time.Duration) (count int, err error) {
	if len(members) == 0 {
		return
	}
	value, err := c.collectionCommand(ctx, "ZREM", key, expiration, members...)
	if err != nil {
		return
	}
	count, err = integerResult(value)
	return
}

// ZRemRangeByRank 删除指定排名闭区间内的成员。
func (c *Client) ZRemRangeByRank(ctx context.Context, key string, start, stop int, expiration ...time.Duration) (count int, err error) {
	value, err := c.collectionCommand(ctx, "ZREMRANGEBYRANK", key, expiration, start, stop)
	if err != nil {
		return
	}
	count, err = integerResult(value)
	return
}

// ZRemRangeByScore 删除指定分数范围内的成员。
func (c *Client) ZRemRangeByScore(ctx context.Context, key, minimum, maximum string, expiration ...time.Duration) (count int, err error) {
	value, err := c.collectionCommand(ctx, "ZREMRANGEBYSCORE", key, expiration, minimum, maximum)
	if err != nil {
		return
	}
	count, err = integerResult(value)
	return
}

// ZScore 返回成员分数，成员不存在时保留 redis.Nil。
func (c *Client) ZScore(ctx context.Context, key string, member any, expiration ...time.Duration) (score float64, err error) {
	value, err := c.collectionCommand(ctx, "ZSCORE", key, expiration, member)
	if err != nil {
		return
	}
	score, err = floatResult(value)
	return
}

func scoredResults(value any, withScores bool) (members []ScoredMember, err error) {
	values, ok := value.([]any)
	if !ok {
		err = errors.New("tkredis: unexpected scored member result")
		return
	}
	if withScores && len(values) > 0 {
		_, nested := values[0].([]any)
		if nested {
			// RESP3 服务器还可能用二元数组表示每一个成员及分数。
			flattened := make([]any, 0, len(values)*2)
			for _, value := range values {
				pair, pairOK := value.([]any)
				if !pairOK || len(pair) != 2 {
					err = errors.New("tkredis: incomplete nested scored member result")
					return
				}
				flattened = append(flattened, pair...)
			}
			values = flattened
		}
	}
	step := 1
	if withScores {
		step = 2
	}
	if len(values)%step != 0 {
		err = errors.New("tkredis: incomplete scored member result")
		return
	}
	members = make([]ScoredMember, 0, len(values)/step)
	for i := 0; i < len(values); i += step {
		text, textOK := values[i].(string)
		if !textOK {
			err = errors.New("tkredis: unexpected member encoding")
			return
		}
		member := ScoredMember{Member: text}
		if withScores {
			// RESP3 可直接返回浮点分数，Lua/RESP2 则返回字符串。
			member.Score, err = floatResult(values[i+1])
			if err != nil {
				return
			}
		}
		members = append(members, member)
	}
	return
}
