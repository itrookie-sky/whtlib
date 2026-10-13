package logx

import (
	"os"
	"sync"
)

// loggerCore 保存单个实例私有的配置和生命周期。
type loggerCore struct {
	mu       sync.RWMutex
	changeMu sync.Mutex
	config   Config
	handler  *handler
	closed   bool
	closeErr error
}

func buildHandler(config Config, previous *handler) (next *handler, resolved Config, retired *fileLease, err error) {
	resolved, err = BuildDefaultConfig(config)
	if err != nil {
		return
	}
	var old *fileLease
	if previous != nil {
		old = previous.state.lease
	}
	state := &outputState{writer: terminalOutput}
	var lease *fileLease
	if resolved.WriteFile {
		var fileConfig FileConfig
		lease, fileConfig, err = sharedFiles.acquire(resolved.File, old)
		if err != nil {
			return
		}
		resolved.File = fileConfig
		state.writer = lease.entry.writer
		state.lease = lease
	}
	next = &handler{state: state, config: resolved}
	if previous != nil {
		previous.state.mu.Lock()
		previous.state.closed = true
		previous.state.mu.Unlock()
		if old != lease {
			retired = old
		}
	}
	return
}

func (c *loggerCore) initialize() (err error) {
	c.mu.RLock()
	ready := c.handler != nil
	closed := c.closed
	c.mu.RUnlock()
	if closed {
		return os.ErrClosed
	}
	if ready {
		return
	}
	c.changeMu.Lock()
	defer c.changeMu.Unlock()
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return os.ErrClosed
	}
	if c.handler != nil {
		return
	}
	next, config, _, err := buildHandler(c.config, nil)
	if err != nil {
		return
	}
	c.handler = next
	c.config = config
	return
}

func (c *loggerCore) setConfig(update Config) (err error) {
	// 串行完成切换与旧资源释放，避免连续更新重复打开同一文件。
	c.changeMu.Lock()
	defer c.changeMu.Unlock()
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return os.ErrClosed
	}
	next, config, retired, err := buildHandler(mergeConfig(c.config, update), c.handler)
	if err == nil {
		c.handler = next
		c.config = config
	}
	c.mu.Unlock()
	if err != nil {
		return
	}
	// 释放配置锁后再等待旧维护任务退出，避免阻塞新配置的打印。
	if retired != nil {
		err = sharedFiles.release(retired)
	}
	return
}

func (c *loggerCore) close() (err error) {
	c.changeMu.Lock()
	defer c.changeMu.Unlock()
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return c.closeErr
	}
	c.closed = true
	var lease *fileLease
	if c.handler != nil {
		c.handler.state.mu.Lock()
		c.handler.state.closed = true
		lease = c.handler.state.lease
		c.handler.state.mu.Unlock()
	}
	c.mu.Unlock()
	if lease != nil {
		c.closeErr = sharedFiles.release(lease)
	}
	return c.closeErr
}
