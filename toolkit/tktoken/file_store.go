package tktoken

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"sync"
	"time"
)

type fileEntry struct {
	Value     []byte    `json:"value"`
	ExpiresAt time.Time `json:"expiresAt,omitzero"`
}

type fileSnapshot struct {
	Version int                  `json:"version"`
	Entries map[string]fileEntry `json:"entries"`
}

// FileStore 将本实例会话保存为独立 JSON 快照，使用绝对到期时间。
//
// 每个文件只能由一个 FileStore 实例持有，不支持多进程共享写入。
// 每次成功写操作都同步持久化，不启动后台任务，也不导出其他缓存。
type FileStore struct {
	mu      sync.Mutex
	path    string
	entries map[string]fileEntry
	now     func() time.Time
}

var _ Store = (*FileStore)(nil)

// NewFileStore 打开本包格式的快照；不自动导入源模块的 .dat 文件。
//
// 父目录必须已经存在。新快照及临时文件权限为 0600，拒绝符号链接和非普通文件。
func NewFileStore(path string) (store *FileStore, err error) {
	if path == "" {
		err = fmt.Errorf("%w: snapshot path is empty", ErrInvalidConfig)
		return
	}
	path, err = filepath.Abs(path)
	if err != nil {
		return
	}
	store = &FileStore{path: path, entries: make(map[string]fileEntry), now: time.Now}
	info, statErr := os.Lstat(path)
	if errors.Is(statErr, os.ErrNotExist) {
		err = store.persist(context.Background(), store.entries)
		if err != nil {
			store = nil
		}
		return
	}
	if statErr != nil {
		store = nil
		err = statErr
		return
	}
	if !info.Mode().IsRegular() {
		store = nil
		err = fmt.Errorf("%w: snapshot must be a regular file", ErrInvalidConfig)
		return
	}
	data, readErr := os.ReadFile(path)
	if readErr != nil {
		store = nil
		err = readErr
		return
	}
	var snapshot fileSnapshot
	err = json.Unmarshal(data, &snapshot)
	if err != nil || snapshot.Version != 1 || snapshot.Entries == nil {
		store = nil
		err = fmt.Errorf("%w: unsupported token snapshot", ErrInvalidConfig)
		return
	}
	err = os.Chmod(path, 0600)
	if err != nil {
		store = nil
		return
	}
	now := store.now()
	for key, entry := range snapshot.Entries {
		if !entry.ExpiresAt.IsZero() && !entry.ExpiresAt.After(now) {
			continue
		}
		store.entries[key] = entry
	}
	return
}

// Get 读取会话副本，已过期的条目视为不存在。
func (s *FileStore) Get(ctx context.Context, key string) (value []byte, found bool, err error) {
	err = ctx.Err()
	if err != nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	entry, found := s.current(key)
	if found {
		value = bytes.Clone(entry.Value)
	}
	return
}

// Set 保存条目和 TTL；写入快照失败时不改变内存中的有效条目。
func (s *FileStore) Set(ctx context.Context, key string, value []byte, ttl time.Duration) (err error) {
	_, err = s.write(ctx, key, nil, value, ttl, "set")
	return
}

// SetIfAbsent 原子创建尚不存在的条目。
func (s *FileStore) SetIfAbsent(ctx context.Context, key string, value []byte, ttl time.Duration) (stored bool, err error) {
	stored, err = s.write(ctx, key, nil, value, ttl, "absent")
	return
}

// CompareAndSwap 仅在现存值与 expected 相同时原子替换条目及 TTL。
func (s *FileStore) CompareAndSwap(ctx context.Context, key string, expected, value []byte, ttl time.Duration) (swapped bool, err error) {
	swapped, err = s.write(ctx, key, expected, value, ttl, "swap")
	return
}

// CompareAndDelete 仅在现存值与 expected 相同时原子删除条目。
func (s *FileStore) CompareAndDelete(ctx context.Context, key string, expected []byte) (deleted bool, err error) {
	deleted, err = s.write(ctx, key, expected, nil, 0, "delete")
	return
}

func (s *FileStore) current(key string) (entry fileEntry, found bool) {
	entry, found = s.entries[key]
	if found && !entry.ExpiresAt.IsZero() && !entry.ExpiresAt.After(s.now()) {
		delete(s.entries, key)
		found = false
	}
	return
}

func (s *FileStore) write(ctx context.Context, key string, expected, value []byte, ttl time.Duration, operation string) (changed bool, err error) {
	err = ctx.Err()
	if err != nil {
		return
	}
	if ttl < 0 {
		err = fmt.Errorf("%w: negative TTL", ErrInvalidConfig)
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	entry, found := s.current(key)
	if operation == "absent" && found {
		return
	}
	if (operation == "swap" || operation == "delete") && (!found || !bytes.Equal(expected, entry.Value)) {
		return
	}
	next := maps.Clone(s.entries)
	if operation == "delete" {
		delete(next, key)
	}
	if operation != "delete" {
		entry = fileEntry{Value: bytes.Clone(value)}
		if ttl > 0 {
			entry.ExpiresAt = s.now().Add(ttl)
		}
		next[key] = entry
	}
	now := s.now()
	maps.DeleteFunc(next, func(_ string, entry fileEntry) (expired bool) {
		return !entry.ExpiresAt.IsZero() && !entry.ExpiresAt.After(now)
	})
	err = s.persist(ctx, next)
	if err != nil {
		return
	}
	s.entries = next
	changed = true
	return
}

func (s *FileStore) persist(ctx context.Context, entries map[string]fileEntry) (err error) {
	data, err := json.Marshal(fileSnapshot{Version: 1, Entries: entries})
	if err != nil {
		return
	}
	file, err := os.CreateTemp(filepath.Dir(s.path), ".tktoken-*.tmp")
	if err != nil {
		return
	}
	defer os.Remove(file.Name())
	defer file.Close()
	err = file.Chmod(0600)
	if err != nil {
		return
	}
	_, err = file.Write(data)
	if err != nil {
		return
	}
	err = file.Sync()
	if err != nil {
		return
	}
	err = file.Close()
	if err != nil {
		return
	}
	err = ctx.Err()
	if err != nil {
		return
	}
	// 同目录重命名确保读者看到完整的新旧快照，不会读到部分写入。
	err = os.Rename(file.Name(), s.path)
	return
}
