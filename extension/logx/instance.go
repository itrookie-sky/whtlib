package logx

import (
	"os"
	"sync"
)

// loggerCore 在派生实例之间共享配置和生命周期。
type loggerCore struct {
	mu       sync.RWMutex
	changeMu sync.Mutex
	config   Config
	handler  *handler
	closed   bool
	closeErr error
}

func buildHandler(config Config, previous *handler) (next *handler, resolved Config, retired *FileWriter, err error) {
	resolved, err = BuildDefaultConfig(config)
	if err != nil {
		return
	}
	state := &outputState{writer: resolved.Writer}
	if resolved.Writer == nil {
		resolved.File, err = normalizeFileConfig(resolved.File)
		if err != nil {
			return
		}
		if previous != nil && previous.state.owned != nil && previous.config.File.Path == resolved.File.Path && previous.config.File.File == resolved.File.File {
			// 同一路径保留唯一的文件 Writer，只修改轮转策略。
			err = previous.state.owned.reconfigure(resolved.File)
			if err != nil {
				return
			}
			state = previous.state
		} else {
			state.owned, err = NewFileWriter(resolved.File)
			if err != nil {
				return
			}
			state.writer = state.owned
		}
	}
	next = &handler{state: state, config: resolved}
	if previous != nil && previous.state != state {
		previous.state.mu.Lock()
		previous.state.closed = true
		previous.state.mu.Unlock()
		retired = previous.state.owned
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
	// 释放配置锁后再等待旧维护任务退出，允许错误回调执行普通打印。
	if retired != nil {
		err = retired.Close()
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
	var owned *FileWriter
	if c.handler != nil {
		c.handler.state.mu.Lock()
		c.handler.state.closed = true
		owned = c.handler.state.owned
		c.handler.state.mu.Unlock()
	}
	c.mu.Unlock()
	if owned != nil {
		c.closeErr = owned.Close()
	}
	return c.closeErr
}
