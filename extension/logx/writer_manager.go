package logx

import (
	"os"
	"path/filepath"
	"sync"
	"time"
)

// fileManager 是进程内唯一的文件资源入口，同一路径只有一个写入和维护任务。
type fileManager struct {
	mu    sync.Mutex
	files map[string]*fileEntry
}

type fileEntry struct {
	key     string
	config  FileConfig
	writer  *fileWriter
	leases  map[*fileLease]func(error)
	closing chan struct{}
}

type fileLease struct{ entry *fileEntry }

var sharedFiles = fileManager{files: make(map[string]*fileEntry)}

func samePolicy(a, b FileConfig) (same bool) {
	return a.RotateSize == b.RotateSize && a.RotateInterval == b.RotateInterval &&
		a.RotateBackupExpire == b.RotateBackupExpire && a.RotateBackupLimit == b.RotateBackupLimit &&
		a.RotateCheckInterval == b.RotateCheckInterval
}

func (m *fileManager) acquire(config FileConfig, previous *fileLease) (lease *fileLease, resolved FileConfig, err error) {
	resolved, err = normalizeFileConfig(config)
	if err != nil {
		return
	}
	err = os.MkdirAll(resolved.Path, 0o750)
	if err != nil {
		return
	}
	resolved.Path, err = filepath.EvalSymlinks(resolved.Path)
	if err != nil {
		return
	}
	pattern, err := compileFilePattern(resolved.File)
	if err != nil {
		return
	}
	key := filepath.Join(resolved.Path, resolved.File)
	for {
		m.mu.Lock()
		entry := m.files[key]
		if entry != nil && entry.closing != nil {
			done := entry.closing
			m.mu.Unlock()
			<-done
			continue
		}
		if entry != nil {
			same := samePolicy(entry.config, resolved)
			own := previous != nil && previous.entry == entry
			if !same && (!own || len(entry.leases) != 1) {
				m.mu.Unlock()
				return nil, resolved, fileConflict("conflicting rotation policy for shared file %q", key)
			}
			if !same {
				internal := resolved
				internal.OnError = func(err error) { m.report(entry, err) }
				err = entry.writer.reconfigure(internal)
				if err != nil {
					m.mu.Unlock()
					return
				}
				entry.config = resolved
			}
			lease = previous
			if !own {
				lease = &fileLease{entry: entry}
			}
			entry.leases[lease] = resolved.OnError
			m.mu.Unlock()
			return
		}
		concretePath := filepath.Join(resolved.Path, pattern.expand(time.Now()))
		info, statErr := os.Stat(concretePath)
		for _, active := range m.files {
			if active.config.Path != resolved.Path {
				continue
			}
			if pattern.overlaps(active.writer.pattern) {
				m.mu.Unlock()
				return nil, resolved, fileConflict("overlapping filename expressions %q and %q", resolved.File, active.config.File)
			}
		}
		// 硬链接和其他别名也不能绕过全局管理器。
		if statErr == nil {
			for _, active := range m.files {
				active.writer.mu.Lock()
				var activeInfo os.FileInfo
				var activeErr error
				if active.writer.file != nil {
					activeInfo, activeErr = active.writer.file.Stat()
				}
				active.writer.mu.Unlock()
				if activeInfo != nil && activeErr == nil && os.SameFile(info, activeInfo) {
					m.mu.Unlock()
					return nil, resolved, fileConflict("file alias %q is already managed as %q", concretePath, active.key)
				}
			}
		}
		entry = &fileEntry{key: key, config: resolved, leases: make(map[*fileLease]func(error))}
		internal := resolved
		internal.OnError = func(err error) { m.report(entry, err) }
		entry.writer, err = newFileWriter(internal)
		if err != nil {
			m.mu.Unlock()
			return
		}
		lease = &fileLease{entry: entry}
		entry.leases[lease] = resolved.OnError
		m.files[key] = entry
		m.mu.Unlock()
		return
	}
}

func (m *fileManager) retain(previous *fileLease) (lease *fileLease) {
	m.mu.Lock()
	defer m.mu.Unlock()
	lease = &fileLease{entry: previous.entry}
	previous.entry.leases[lease] = previous.entry.leases[previous]
	return
}

func (m *fileManager) report(entry *fileEntry, err error) {
	m.mu.Lock()
	callbacks := make([]func(error), 0, len(entry.leases))
	for _, callback := range entry.leases {
		callbacks = append(callbacks, callback)
	}
	m.mu.Unlock()
	for _, callback := range callbacks {
		callback(err)
	}
}

func (m *fileManager) release(lease *fileLease) (err error) {
	m.mu.Lock()
	entry := lease.entry
	delete(entry.leases, lease)
	if len(entry.leases) != 0 {
		m.mu.Unlock()
		return
	}
	entry.closing = make(chan struct{})
	m.mu.Unlock()
	// 不持全局锁等待维护退出，防止错误通知获取全局锁时死锁。
	err = entry.writer.Close()
	m.mu.Lock()
	delete(m.files, entry.key)
	close(entry.closing)
	m.mu.Unlock()
	return
}
