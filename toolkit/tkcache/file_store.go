package tkcache

import (
	"context"
	"encoding/json"
	"errors"
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

// fileStore 复用内存操作，每次修改在快照持久化成功后提交。
// 一个文件只能由一个实例持有，不提供跨实例或跨进程文件锁。
type fileStore struct {
	mu     sync.Mutex
	path   string
	memory *memoryStore
}

func newFileStore(path string) (store *fileStore, err error) {
	if path == "" {
		err = invalidConfig("snapshot path is empty")
		return
	}
	path, err = filepath.Abs(path)
	if err != nil {
		return
	}
	store = &fileStore{path: path, memory: &memoryStore{entries: make(map[string]entry), now: time.Now}}
	info, statErr := os.Lstat(path)
	if errors.Is(statErr, os.ErrNotExist) {
		err = store.persist(context.Background(), store.memory.entries)
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
		err = invalidConfig("snapshot must be a regular file")
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
		err = invalidConfig("unsupported cache snapshot")
		return
	}
	err = os.Chmod(path, 0600)
	if err != nil {
		store = nil
		return
	}
	now := store.memory.now()
	for key, item := range snapshot.Entries {
		if !item.ExpiresAt.IsZero() && !item.ExpiresAt.After(now) {
			continue
		}
		store.memory.entries[key] = entry{value: copyValue(item.Value), expires: item.ExpiresAt}
	}
	return
}

func (s *fileStore) get(ctx context.Context, key string) (value []byte, found bool, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.memory.get(ctx, key)
}

func (s *fileStore) ttl(ctx context.Context, key string) (ttl time.Duration, found bool, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.memory.ttl(ctx, key)
}

func (s *fileStore) apply(ctx context.Context, key string, req request) (result outcome, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	next := &memoryStore{entries: maps.Clone(s.memory.entries), now: s.memory.now}
	result, err = next.apply(ctx, key, req)
	if err != nil || !result.changed {
		return
	}
	err = s.commit(ctx, next)
	if err != nil {
		return outcome{}, err
	}
	return
}

func (s *fileStore) clear(ctx context.Context, prefix string) (err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	next := &memoryStore{entries: maps.Clone(s.memory.entries), now: s.memory.now}
	err = next.clear(ctx, prefix)
	if err != nil {
		return
	}
	return s.commit(ctx, next)
}

// commit 只在成功替换磁盘快照后公布内存数据。
func (s *fileStore) commit(ctx context.Context, next *memoryStore) (err error) {
	now := next.now()
	maps.DeleteFunc(next.entries, func(_ string, item entry) (expired bool) {
		return !item.expires.IsZero() && !item.expires.After(now)
	})
	err = s.persist(ctx, next.entries)
	if err != nil {
		return
	}
	s.memory.entries = next.entries
	return
}

func (s *fileStore) close() (err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	// 文件实例没有清理协程；关闭只释放内存，保留磁盘快照。
	clear(s.memory.entries)
	return
}

func (s *fileStore) persist(ctx context.Context, entries map[string]entry) (err error) {
	snapshot := fileSnapshot{Version: 1, Entries: make(map[string]fileEntry, len(entries))}
	for key, item := range entries {
		snapshot.Entries[key] = fileEntry{Value: item.value, ExpiresAt: item.expires}
	}
	data, err := json.Marshal(snapshot)
	if err != nil {
		return
	}
	file, err := os.CreateTemp(filepath.Dir(s.path), ".tkcache-*.tmp")
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
