package tkredis

import (
	"context"
	"errors"
	"os"
	"strings"
	"time"

	"github.com/itrookie-sky/whtlib/extension/filepathx"

	"github.com/redis/go-redis/v9"
)

// RegisterScript 并发安全地注册或替换脚本，不访问服务器。
func (c *Client) RegisterScript(name, script string) (err error) {
	err = c.checkOpen()
	if err != nil {
		return
	}
	if name == "" || strings.TrimSpace(script) == "" {
		err = invalidArgument("script name and source are required")
		return
	}
	c.scriptMu.Lock()
	c.scripts[name] = script
	c.scriptMu.Unlock()
	return
}

// ScriptLoad 注册脚本并在当前服务器加载；连接失败作为错误返回。
func (c *Client) ScriptLoad(ctx context.Context, name, script string) (err error) {
	err = c.RegisterScript(name, script)
	if err != nil {
		return
	}
	_, err = c.native.ScriptLoad(nonNilContext(ctx), script).Result()
	return
}

// ScriptLoadByPath 读取 Lua 文件，以去掉扩展名的文件名注册并加载。
func (c *Client) ScriptLoadByPath(ctx context.Context, scriptPath string) (err error) {
	script, err := os.ReadFile(scriptPath)
	if err != nil {
		return
	}
	name := filepathx.Stem(scriptPath)
	err = c.ScriptLoad(ctx, name, string(script))
	return
}

// Eval 执行脚本，JSON 参数需要显式包装；原样返回 redis.Nil。
func (c *Client) Eval(ctx context.Context, script string, keys []string, args ...any) (value any, err error) {
	err = c.checkOpen()
	if err != nil {
		return
	}
	converted, err := explicitArgs(args)
	if err != nil {
		return
	}
	value, err = c.native.Eval(nonNilContext(ctx), script, keys, converted...).Result()
	return
}

// EvalSha 按名称执行脚本，NOSCRIPT 时通过原始脚本重载并执行。
func (c *Client) EvalSha(ctx context.Context, name string, keys []string, args ...any) (value any, err error) {
	err = c.checkOpen()
	if err != nil {
		return
	}
	c.scriptMu.RLock()
	script, ok := c.scripts[name]
	c.scriptMu.RUnlock()
	if !ok {
		err = scriptNotFound(name)
		return
	}
	return c.runScript(ctx, redis.NewScript(script), keys, args...)
}

func (c *Client) runScript(ctx context.Context, script *redis.Script, keys []string, args ...any) (value any, err error) {
	err = c.checkOpen()
	if err != nil {
		return
	}
	converted, err := explicitArgs(args)
	if err != nil {
		return
	}
	value, err = script.Run(nonNilContext(ctx), c.native, keys, converted...).Result()
	return
}

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

// Cad 原子比较字符串值，仅在一致时删除；复杂值需使用 JSON 包装。
func (c *Client) Cad(ctx context.Context, key string, expected any) (ok bool, err error) {
	value, err := c.Eval(ctx, compareDeleteScript, []string{key}, expected)
	if err != nil {
		return
	}
	count, err := integerResult(value)
	ok = count == 1
	return
}

// CD 原子检测冷却状态；兼容清理无到期时间的旧标记，本次仍视为冷却中。
func (c *Client) CD(ctx context.Context, key string) (ready bool, err error) {
	value, err := c.Eval(ctx, `
local ttl = redis.call('PTTL', KEYS[1])
if ttl == -2 then return 1 end
if ttl == -1 then redis.call('DEL', KEYS[1]) end
return 0`, []string{key})
	if err != nil {
		return
	}
	count, err := integerResult(value)
	ready = count == 1
	return
}

// SetCD 原子占用冷却标记，保留毫秒精度。
func (c *Client) SetCD(ctx context.Context, key string, cooldown time.Duration) (ok bool, err error) {
	err = c.checkOpen()
	if err != nil {
		return
	}
	milliseconds, err := positiveMilliseconds(cooldown)
	if err != nil {
		return
	}
	value, err := c.Do(ctx, "SET", key, "1", "PX", milliseconds, "NX")
	if errors.Is(err, redis.Nil) {
		err = nil
		return
	}
	if err != nil {
		return
	}
	ok = value == "OK"
	return
}

// Once 原子占用幂等标记，默认保留七天；不承诺业务 exactly-once。
func (c *Client) Once(ctx context.Context, key string, expiration ...time.Duration) (ok bool, err error) {
	ttl := 7 * 24 * time.Hour
	if len(expiration) > 0 {
		ttl = expiration[0]
	}
	return c.SetCD(ctx, key, ttl)
}

// Polling 原子递增并在 [0, maximum) 内循环。
func (c *Client) Polling(ctx context.Context, key string, maximum int) (index int, err error) {
	if maximum <= 0 {
		err = invalidArgument("polling maximum must be positive")
		return
	}
	value, err := c.Eval(ctx, `
local next = redis.call('INCR', KEYS[1])
if next > tonumber(ARGV[1]) then
  redis.call('SET', KEYS[1], 1)
  return 0
end
return next - 1`, []string{key}, maximum)
	if err != nil {
		return
	}
	index, err = integerResult(value)
	return
}

func positiveMilliseconds(ttl time.Duration) (milliseconds int64, err error) {
	if ttl <= 0 {
		err = invalidArgument("expiration must be positive")
		return
	}
	return ttlMilliseconds(ttl)
}

func integerResult(value any) (result int, err error) {
	command := redis.NewCmd(context.Background())
	command.SetVal(value)
	result, err = command.Int()
	return
}

func floatResult(value any) (result float64, err error) {
	if number, ok := value.(float64); ok {
		return number, nil
	}
	command := redis.NewCmd(context.Background())
	command.SetVal(value)
	result, err = command.Float64()
	return
}

func stringResults(value any) (result []string, err error) {
	command := redis.NewCmd(context.Background())
	command.SetVal(value)
	result, err = command.StringSlice()
	return
}
