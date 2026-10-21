// Package tkcache 提供统一的内存、Redis 与文件字节缓存。
package tkcache

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"hash/fnv"
	"math"
	"regexp"
	"sync"
	"time"

	"github.com/itrookie-sky/whtlib/toolkit/tkredis"
)

// StoreType 指定实例使用的 Store 类型。
type StoreType string

const (
	MemoryStore StoreType = "memory"
	RedisStore  StoreType = "redis"
	FileStore   StoreType = "file"
)

var (
	ErrClosed          = errors.New("tkcache: closed")
	ErrInvalidArgument = errors.New("tkcache: invalid argument")
	ErrInvalidConfig   = errors.New("tkcache: invalid config")
	ErrLoaderPanic     = errors.New("tkcache: loader panic")
	namespacePattern   = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)
)

// Config 配置独立缓存，RedisClient 始终由调用方管理。
// FilePath 仅用于 FileStore，一个文件只能由一个活动 Cache 实例持有。
type Config struct {
	Store           StoreType
	Namespace       string
	CleanupInterval time.Duration
	RedisClient     *tkredis.Client
	FilePath        string
}

// Loader 在未命中时提供字节数据。
type Loader func(ctx context.Context) (value []byte, err error)

// LoadOption 配置回源结果的缓存策略。
type LoadOption func(options *loadOptions)
type loadOptions struct{ cacheEmpty bool }

// WithCacheEmpty 控制是否缓存 nil 回源结果，默认 true。
func WithCacheEmpty(enabled bool) (option LoadOption) {
	return func(options *loadOptions) { options.cacheEmpty = enabled }
}

func buildLoadOptions(options []LoadOption) (result loadOptions) {
	result.cacheEmpty = true
	for _, option := range options {
		if option != nil {
			option(&result)
		}
	}
	return
}

type operation string

const (
	opSet       operation = "set"
	opSetNX     operation = "nx"
	opGetDelete operation = "getdel"
	opGetExpire operation = "getexpire"
	opGetSet    operation = "getset"
	opCAS       operation = "cas"
	opCAD       operation = "cad"
)

type request struct {
	operation       operation
	value, expected []byte
	ttl             time.Duration
}
type outcome struct {
	value          []byte
	found, changed bool
}
type store interface {
	get(context.Context, string) (value []byte, found bool, err error)
	apply(context.Context, string, request) (result outcome, err error)
	ttl(context.Context, string) (ttl time.Duration, found bool, err error)
	clear(context.Context, string) (err error)
	close() (err error)
}

// Cache 管理一个 Namespace；不同内存实例的数据彼此隔离。
type Cache struct {
	store   store
	prefix  string
	gate    sync.RWMutex
	stripes [64]sync.Mutex
	mu      sync.Mutex
	closed  bool
	flights map[string]*flight
	active  map[*flight]struct{}
}

// New 创建缓存实例，不探测 Redis 网络，也不关闭借入的客户端。
func New(config Config) (cache *Cache, err error) {
	if !namespacePattern.MatchString(config.Namespace) || config.CleanupInterval < 0 {
		return nil, ErrInvalidConfig
	}
	cache = &Cache{prefix: "tkcache:" + config.Namespace + ":", flights: make(map[string]*flight), active: make(map[*flight]struct{})}
	switch config.Store {
	case "", MemoryStore:
		interval := config.CleanupInterval
		if interval == 0 {
			interval = time.Minute
		}
		cache.store = newMemoryStore(interval)
	case RedisStore:
		if config.RedisClient == nil || config.RedisClient.Raw() == nil {
			return nil, ErrInvalidConfig
		}
		cache.store = &redisStore{client: config.RedisClient.Raw()}
	case FileStore:
		cache.store, err = newFileStore(config.FilePath)
		if err != nil {
			return nil, err
		}
	default:
		return nil, ErrInvalidConfig
	}
	return
}

func normalizeTTL(ttl time.Duration) (result time.Duration, err error) {
	if ttl < 0 {
		return 0, ErrInvalidArgument
	}
	if ttl == 0 {
		return
	}
	milliseconds := int64(ttl / time.Millisecond)
	if ttl%time.Millisecond != 0 {
		milliseconds++
	}
	if milliseconds > math.MaxInt64/int64(time.Millisecond) {
		return 0, ErrInvalidArgument
	}
	return time.Duration(milliseconds) * time.Millisecond, nil
}

func (c *Cache) check(ctx context.Context) (err error) {
	if ctx == nil {
		panic("tkcache: nil context")
	}
	c.mu.Lock()
	closed := c.closed
	c.mu.Unlock()
	if closed {
		return ErrClosed
	}
	return ctx.Err()
}

func (c *Cache) dataKey(key string) (physical string, err error) {
	if key == "" {
		return "", ErrInvalidArgument
	}
	return c.prefix + "data:" + key, nil
}

func (c *Cache) stripe(key string) (lock *sync.Mutex) {
	hash := fnv.New32a()
	_, _ = hash.Write([]byte(key))
	return &c.stripes[hash.Sum32()%uint32(len(c.stripes))]
}

// Get 读取数据，未命中与空内容通过 found 区分。
func (c *Cache) Get(ctx context.Context, key string) (value []byte, found bool, err error) {
	physical, err := c.dataKey(key)
	if err != nil {
		return
	}
	c.gate.RLock()
	defer c.gate.RUnlock()
	err = c.check(ctx)
	if err != nil {
		return
	}
	return c.store.get(ctx, physical)
}

func (c *Cache) mutate(ctx context.Context, key string, req request) (result outcome, err error) {
	physical, err := c.dataKey(key)
	if err != nil {
		return
	}
	return c.mutatePhysical(ctx, key, physical, req)
}

func (c *Cache) mutatePhysical(ctx context.Context, key, physical string, req request) (result outcome, err error) {
	req.ttl, err = normalizeTTL(req.ttl)
	if err != nil {
		return
	}
	c.gate.RLock()
	defer c.gate.RUnlock()
	lock := c.stripe(physical)
	lock.Lock()
	defer lock.Unlock()
	err = c.check(ctx)
	if err != nil {
		return
	}
	result, err = c.store.apply(ctx, physical, req)
	if (result.changed || req.operation == opGetDelete) && key != "" {
		c.mu.Lock()
		delete(c.flights, key)
		c.mu.Unlock()
	}
	return
}

// Set 覆盖数据并重新计算 TTL，0 表示不设置到期时间。
func (c *Cache) Set(ctx context.Context, key string, value []byte, ttl time.Duration) (err error) {
	_, err = c.mutate(ctx, key, request{operation: opSet, value: bytes.Clone(value), ttl: ttl})
	return
}

// SetIfAbsent 原子写入不存在或已过期的数据。
func (c *Cache) SetIfAbsent(ctx context.Context, key string, value []byte, ttl time.Duration) (stored bool, err error) {
	result, err := c.mutate(ctx, key, request{operation: opSetNX, value: bytes.Clone(value), ttl: ttl})
	return result.changed, err
}

// GetAndExpire 原子读取并重设 TTL，0 表示移除到期时间。
func (c *Cache) GetAndExpire(ctx context.Context, key string, ttl time.Duration) (value []byte, found bool, err error) {
	result, err := c.mutate(ctx, key, request{operation: opGetExpire, ttl: ttl})
	return result.value, result.found, err
}

// GetAndDelete 原子读取并删除数据。
func (c *Cache) GetAndDelete(ctx context.Context, key string) (value []byte, found bool, err error) {
	result, err := c.mutate(ctx, key, request{operation: opGetDelete})
	return result.value, result.found, err
}

// GetOrSet 返回现有内容，或原子写入并返回候选内容；命中不续期。
func (c *Cache) GetOrSet(ctx context.Context, key string, value []byte, ttl time.Duration) (result []byte, err error) {
	out, err := c.mutate(ctx, key, request{operation: opGetSet, value: bytes.Clone(value), ttl: ttl})
	return out.value, err
}

// CompareAndSwap 仅在原始字节匹配时原子替换数据及 TTL。
func (c *Cache) CompareAndSwap(ctx context.Context, key string, expected, value []byte, ttl time.Duration) (swapped bool, err error) {
	result, err := c.mutate(ctx, key, request{operation: opCAS, expected: bytes.Clone(expected), value: bytes.Clone(value), ttl: ttl})
	return result.changed, err
}

// CompareAndDelete 仅删除内容匹配的数据，未命中不成功。
func (c *Cache) CompareAndDelete(ctx context.Context, key string, expected []byte) (deleted bool, err error) {
	result, err := c.mutate(ctx, key, request{operation: opCAD, expected: bytes.Clone(expected)})
	return result.changed, err
}

// Once 在独立区域创建有期限占位，不表示业务操作 exactly-once。
func (c *Cache) Once(ctx context.Context, key string, ttl time.Duration) (acquired bool, err error) {
	if key == "" || ttl <= 0 {
		return false, ErrInvalidArgument
	}
	result, err := c.mutatePhysical(ctx, "", c.prefix+"once:"+key, request{operation: opSetNX, value: []byte("1"), ttl: ttl})
	return result.changed, err
}

// TTL 返回剩余 TTL；永久数据返回 0、true，未命中返回 0、false。
func (c *Cache) TTL(ctx context.Context, key string) (ttl time.Duration, found bool, err error) {
	physical, err := c.dataKey(key)
	if err != nil {
		return
	}
	c.gate.RLock()
	defer c.gate.RUnlock()
	err = c.check(ctx)
	if err != nil {
		return
	}
	return c.store.ttl(ctx, physical)
}

// Exists 判断当前是否有未过期的数据。
func (c *Cache) Exists(ctx context.Context, key string) (found bool, err error) {
	_, found, err = c.Get(ctx, key)
	return
}

// Delete 删除指定数据；跨 key 不承诺原子性，错误保留已完成的操作。
func (c *Cache) Delete(ctx context.Context, keys ...string) (err error) {
	err = c.check(ctx)
	if err != nil {
		return
	}
	for _, key := range keys {
		if key == "" {
			return ErrInvalidArgument
		}
	}
	for _, key := range keys {
		_, _, currentErr := c.GetAndDelete(ctx, key)
		err = errors.Join(err, currentErr)
	}
	return
}

// GetMap 批量读取；缺失项不进入结果，出错时保留已读取的项。
func (c *Cache) GetMap(ctx context.Context, keys []string) (values map[string][]byte, err error) {
	values = make(map[string][]byte, len(keys))
	err = c.check(ctx)
	if err != nil {
		return
	}
	for _, key := range keys {
		if key == "" {
			return values, ErrInvalidArgument
		}
	}
	for _, key := range keys {
		value, found, currentErr := c.Get(ctx, key)
		err = errors.Join(err, currentErr)
		if currentErr == nil && found {
			values[key] = value
		}
	}
	return
}

// SetMap 批量写入；错误时可能部分完成，不承诺跨 key 事务。
func (c *Cache) SetMap(ctx context.Context, values map[string][]byte, ttl time.Duration) (err error) {
	_, err = normalizeTTL(ttl)
	if err != nil {
		return
	}
	err = c.check(ctx)
	if err != nil {
		return
	}
	for key := range values {
		if key == "" {
			return ErrInvalidArgument
		}
	}
	for key, value := range values {
		err = errors.Join(err, c.Set(ctx, key, value, ttl))
	}
	return
}

// Clear 清理当前 Namespace，包括 Once；Redis 并发外部写入不属于快照事务。
func (c *Cache) Clear(ctx context.Context) (err error) {
	c.gate.Lock()
	defer c.gate.Unlock()
	err = c.check(ctx)
	if err != nil {
		return
	}
	c.mu.Lock()
	clear(c.flights)
	c.mu.Unlock()
	return c.store.clear(ctx, c.prefix)
}

// Close 终止维护与等待者，释放内存，不关闭外部 Redis 客户端。
func (c *Cache) Close() (err error) {
	c.gate.Lock()
	defer c.gate.Unlock()
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return
	}
	c.closed = true
	cancels := make([]context.CancelFunc, 0, len(c.active))
	for f := range c.active {
		f.finished = true
		f.err = ErrClosed
		close(f.done)
		if f.stop != nil {
			f.stop()
		}
		cancels = append(cancels, f.cancel)
	}
	clear(c.flights)
	clear(c.active)
	c.mu.Unlock()
	for _, cancel := range cancels {
		cancel()
	}
	return c.store.close()
}

// String 返回不包含数据与连接认证信息的实例标识。
func (c *Cache) String() (result string) { return fmt.Sprintf("tkcache(%s)", c.prefix) }
