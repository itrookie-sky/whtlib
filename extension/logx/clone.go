package logx

import "os"

// Clone 克隆全局 Logger 的当前配置，返回需要独立 Close 的实例。
func Clone() (logger *Logger, err error) {
	return Default().Clone()
}

// Clone 创建当前配置的独立快照，继承调用跳过层数并共享文件写入资源。
// 后续 SetConfig、Close 各自独立；已关闭实例返回 os.ErrClosed。
// nil 或未初始化的 Logger 返回 ErrInvalidConfig。
func (l *Logger) Clone() (logger *Logger, err error) {
	if l == nil || l.core == nil {
		return nil, invalidConfig("cannot clone nil or uninitialized logger")
	}
	l.core.mu.RLock()
	defer l.core.mu.RUnlock()
	if l.core.closed {
		return nil, os.ErrClosed
	}
	return l.cloneLocked(l.skip)
}

// cloneLocked 复制配置、实例状态和文件引用；调用方必须持有 core 的读锁。
// 保留 closed 状态供原有 Skip 使用，Clone 自行拒绝已关闭实例。
func (l *Logger) cloneLocked(skip int) (logger *Logger, err error) {
	config, err := BuildDefaultConfig(l.core.config)
	if err != nil {
		return
	}
	logger = &Logger{core: &loggerCore{config: config, closed: l.core.closed}, skip: skip}
	if l.core.handler == nil || l.core.closed {
		return
	}
	var lease *fileLease
	if l.core.handler.state.lease != nil {
		lease = sharedFiles.retain(l.core.handler.state.lease)
	}
	logger.core.handler = &handler{config: config, state: &outputState{writer: l.core.handler.state.writer, lease: lease}}
	return
}
