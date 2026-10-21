package tkcache

import (
	"bytes"
	"context"
	"strings"
	"sync"
	"time"
)

type entry struct {
	value   []byte
	expires time.Time
}
type memoryStore struct {
	mu         sync.Mutex
	entries    map[string]entry
	stop, done chan struct{}
}

func newMemoryStore(interval time.Duration) (store *memoryStore) {
	store = &memoryStore{entries: make(map[string]entry), stop: make(chan struct{}), done: make(chan struct{})}
	go store.cleanup(interval)
	return
}

func (m *memoryStore) cleanup(interval time.Duration) {
	defer close(m.done)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-m.stop:
			return
		case now := <-ticker.C:
			m.mu.Lock()
			for key, item := range m.entries {
				if !item.expires.IsZero() && !now.Before(item.expires) {
					delete(m.entries, key)
				}
			}
			m.mu.Unlock()
		}
	}
}

func (m *memoryStore) lookup(key string) (item entry, found bool) {
	item, found = m.entries[key]
	if found && !item.expires.IsZero() && !time.Now().Before(item.expires) {
		delete(m.entries, key)
		found = false
	}
	return
}

func copyValue(value []byte) (result []byte) {
	if value == nil {
		return []byte{}
	}
	return bytes.Clone(value)
}

func (m *memoryStore) put(key string, value []byte, ttl time.Duration) {
	item := entry{value: copyValue(value)}
	if ttl > 0 {
		item.expires = time.Now().Add(ttl)
	}
	m.entries[key] = item
}

func (m *memoryStore) get(ctx context.Context, key string) (value []byte, found bool, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	err = ctx.Err()
	if err != nil {
		return
	}
	item, found := m.lookup(key)
	if found {
		value = copyValue(item.value)
	}
	return
}

func (m *memoryStore) apply(ctx context.Context, key string, req request) (result outcome, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	err = ctx.Err()
	if err != nil {
		return
	}
	item, found := m.lookup(key)
	switch req.operation {
	case opSet:
		m.put(key, req.value, req.ttl)
		result.changed = true
	case opSetNX:
		if !found {
			m.put(key, req.value, req.ttl)
			result.changed = true
		}
	case opGetSet:
		if !found {
			m.put(key, req.value, req.ttl)
			item, _ = m.lookup(key)
			result.changed = true
		}
		result.value = copyValue(item.value)
		result.found = true
	case opGetDelete:
		if found {
			result = outcome{value: copyValue(item.value), found: true, changed: true}
			delete(m.entries, key)
		}
	case opGetExpire:
		if found {
			m.put(key, item.value, req.ttl)
			result = outcome{value: copyValue(item.value), found: true, changed: true}
		}
	case opCAS:
		if found && bytes.Equal(item.value, req.expected) {
			m.put(key, req.value, req.ttl)
			result.changed = true
		}
	case opCAD:
		if found && bytes.Equal(item.value, req.expected) {
			delete(m.entries, key)
			result.changed = true
		}
	default:
		err = ErrInvalidArgument
	}
	return
}

func (m *memoryStore) ttl(ctx context.Context, key string) (ttl time.Duration, found bool, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	err = ctx.Err()
	if err != nil {
		return
	}
	item, found := m.lookup(key)
	if found && !item.expires.IsZero() {
		ttl = max(time.Until(item.expires), 0)
	}
	return
}

func (m *memoryStore) clear(ctx context.Context, prefix string) (err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	err = ctx.Err()
	if err != nil {
		return
	}
	for key := range m.entries {
		if strings.HasPrefix(key, prefix) {
			delete(m.entries, key)
		}
	}
	return
}

func (m *memoryStore) close() (err error) {
	close(m.stop)
	<-m.done
	m.mu.Lock()
	clear(m.entries)
	m.mu.Unlock()
	return
}
