// Package tkredis 提供不依赖框架的 Redis 命令、脚本、集合与分布式锁。
package tkredis

import (
	"context"
	"errors"
	"sync"

	"github.com/itrookie-sky/whtlib/adapter"
	"github.com/itrookie-sky/whtlib/extension/reflectx"

	"github.com/redis/go-redis/v9"
)

// ClientConfig 保留 go-redis 单节点配置的原始字段类型。
type ClientConfig = redis.Options

// ClientConfigOption 修改客户端配置。
type ClientConfigOption func(config *ClientConfig)

// Client 管理命令入口、脚本和自身创建的维护任务。
type Client struct {
	native    redis.UniversalClient
	owned     bool
	ctx       context.Context
	cancel    context.CancelFunc
	mu        sync.Mutex
	closed    bool
	wg        sync.WaitGroup
	closeDone chan struct{}
	closeErr  error
	logger    adapter.Logger
	scriptMu  sync.RWMutex
	scripts   map[string]string
}

// New 创建并拥有底层连接池；首次命令才连接服务器。
func New(config *redis.Options) (client *Client, err error) {
	options := redis.Options{}
	if config != nil {
		options = *config
	}
	client = newClient(redis.NewClient(&options), true)
	return
}

// NewWithOption 使用配置函数创建客户端。
func NewWithOption(options ...ClientConfigOption) (client *Client, err error) {
	config := &redis.Options{}
	for _, option := range options {
		if option != nil {
			option(config)
		}
	}
	return New(config)
}

// Wrap 借用现有连接池，Close 不会关闭该连接池。
func Wrap(native redis.UniversalClient) (client *Client, err error) {
	if reflectx.IsNil(native) {
		err = ErrInvalidClient
		return
	}
	client = newClient(native, false)
	return
}

func newClient(native redis.UniversalClient, owned bool) (client *Client) {
	ctx, cancel := context.WithCancel(context.Background())
	client = &Client{
		native: native, owned: owned, ctx: ctx, cancel: cancel,
		closeDone: make(chan struct{}), scripts: make(map[string]string),
	}
	return
}

// Raw 返回原生连接池；调用方需遵守其原有生命周期。
func (c *Client) Raw() (native redis.UniversalClient) {
	return c.native
}

// SetLogger 注入可选的并发安全日志实现；不会记录认证信息、锁 key 或完整参数。
func (c *Client) SetLogger(logger adapter.Logger) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if reflectx.IsNil(logger) {
		c.logger = nil
		return
	}
	c.logger = logger
}

func (c *Client) warnMaintenance(ctx context.Context) {
	c.mu.Lock()
	logger := c.logger
	c.mu.Unlock()
	if logger != nil {
		logger.Warn(nonNilContext(ctx), "Redis lock maintenance stopped after a lease error")
	}
}

func (c *Client) checkOpen() (err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		err = ErrClosed
	}
	return
}

func (c *Client) beginTask() (err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		err = ErrClosed
		return
	}
	c.wg.Add(1)
	return
}

// Close 幂等停止锁维护，等待释放租约，再关闭自有连接池。
func (c *Client) Close() (err error) {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		<-c.closeDone
		return c.closeErr
	}
	c.closed = true
	c.cancel()
	c.mu.Unlock()
	c.wg.Wait()
	if c.owned {
		c.closeErr = c.native.Close()
	}
	close(c.closeDone)
	return c.closeErr
}

func nonNilContext(ctx context.Context) (result context.Context) {
	if ctx == nil {
		return context.Background()
	}
	return ctx
}

// Do 执行原生命令，保留 redis.Nil 和原始错误；JSON 参数需要显式包装。
func (c *Client) Do(ctx context.Context, command string, args ...any) (value any, err error) {
	err = c.checkOpen()
	if err != nil {
		return
	}
	if command == "" {
		err = ErrInvalidCommand
		return
	}
	converted, err := explicitArgs(args)
	if err != nil {
		return
	}
	commandArgs := make([]any, 0, len(converted)+1)
	commandArgs = append(commandArgs, command)
	commandArgs = append(commandArgs, converted...)
	value, err = c.native.Do(nonNilContext(ctx), commandArgs...).Result()
	return
}

// DoJSON 显式将复杂参数转换为 JSON，再执行命令。
func (c *Client) DoJSON(ctx context.Context, command string, args ...any) (value any, err error) {
	converted, err := EncodeArgs(0, args...)
	if err != nil {
		return
	}
	return c.Do(ctx, command, converted...)
}

// Result 保存一条管道命令的原始值与错误。
type Result struct {
	Data any
	Err  error
}

// IsNil 仅将 Redis nil 视为未命中，空字符串和空数组均保留。
func (r Result) IsNil() (ok bool) {
	return errors.Is(r.Err, redis.Nil)
}

// Pipeline 顺序返回每条命令的结果；部分失败时仍返回全部结果。
func (c *Client) Pipeline(ctx context.Context, commands ...[]any) (results []Result, err error) {
	err = c.checkOpen()
	if err != nil {
		return
	}
	ctx = nonNilContext(ctx)
	prepared := make([][]any, len(commands))
	for i, command := range commands {
		if len(command) == 0 {
			err = invalidCommand(i)
			return
		}
		prepared[i], err = explicitArgs(command)
		if err != nil {
			return
		}
	}
	pipeline := c.native.Pipeline()
	queued := make([]*redis.Cmd, 0, len(prepared))
	for _, command := range prepared {
		queued = append(queued, pipeline.Do(ctx, command...))
	}
	_, err = pipeline.Exec(ctx)
	results = make([]Result, len(queued))
	for i, command := range queued {
		results[i].Data, results[i].Err = command.Result()
	}
	return
}
