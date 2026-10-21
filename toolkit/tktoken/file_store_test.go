package tktoken

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestFileStoreAtomicOperationsAndRestartExpiry(t *testing.T) {
	ctx := t.Context()
	path := filepath.Join(t.TempDir(), "sessions.json")
	store, err := NewFileStore(path)
	if err != nil {
		t.Fatal(err)
	}
	value := []byte("first")
	stored, err := store.SetIfAbsent(ctx, "session", value, time.Hour)
	if err != nil || !stored {
		t.Fatal("无法创建会话")
	}
	value[0] = 'x'
	read, found, err := store.Get(ctx, "session")
	if err != nil || !found || string(read) != "first" {
		t.Fatal("存储不应共享输入字节")
	}
	read[0] = 'x'
	stored, err = store.SetIfAbsent(ctx, "session", []byte("second"), time.Hour)
	if err != nil || stored {
		t.Fatal("重复创建覆盖了会话")
	}
	swapped, err := store.CompareAndSwap(ctx, "session", []byte("wrong"), []byte("second"), time.Hour)
	if err != nil || swapped {
		t.Fatal("比较失败时不应更新")
	}
	swapped, err = store.CompareAndSwap(ctx, "session", []byte("first"), []byte("second"), 2*time.Hour)
	if err != nil || !swapped {
		t.Fatal("比较成功时应更新")
	}
	expiry := store.entries["session"].ExpiresAt
	reopened, err := NewFileStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if !reopened.entries["session"].ExpiresAt.Equal(expiry) {
		t.Fatal("重新加载不应延长绝对到期时间")
	}
	read, found, err = reopened.Get(ctx, "session")
	if err != nil || !found || string(read) != "second" {
		t.Fatal("重新加载丢失会话")
	}
	deleted, err := reopened.CompareAndDelete(ctx, "session", []byte("first"))
	if err != nil || deleted {
		t.Fatal("旧值不能删除当前值")
	}
	deleted, err = reopened.CompareAndDelete(ctx, "session", []byte("second"))
	if err != nil || !deleted {
		t.Fatal("当前值应可以删除")
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("快照权限必须为 0600")
	}
	// 通过过去的时钟生成合法快照，重启后必须过滤已到期条目。
	store, err = NewFileStore(path)
	if err != nil {
		t.Fatal(err)
	}
	past := time.Now().Add(-2 * time.Hour)
	store.now = func() (value time.Time) { return past }
	err = store.Set(ctx, "expired", []byte("data"), time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	reopened, err = NewFileStore(path)
	if err != nil {
		t.Fatal(err)
	}
	_, found, err = reopened.Get(ctx, "expired")
	if err != nil || found {
		t.Fatal("重启后不应恢复过期会话")
	}
}

func TestFileStoreConcurrentCreateAndLiveExpiry(t *testing.T) {
	ctx := t.Context()
	store, err := NewFileStore(filepath.Join(t.TempDir(), "sessions.json"))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	store.now = func() (value time.Time) { return now }
	results := make(chan bool, 16)
	errorsCh := make(chan error, 16)
	var group sync.WaitGroup
	for range 16 {
		group.Go(func() {
			stored, setErr := store.SetIfAbsent(ctx, "session", []byte("value"), time.Hour)
			results <- stored
			errorsCh <- setErr
		})
	}
	group.Wait()
	close(results)
	close(errorsCh)
	for setErr := range errorsCh {
		if setErr != nil {
			t.Fatal(setErr)
		}
	}
	count := 0
	for stored := range results {
		if stored {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("并发创建成功次数 = %d", count)
	}
	now = now.Add(time.Hour)
	_, found, err := store.Get(ctx, "session")
	if err != nil || found {
		t.Fatal("到期条目应视为不存在")
	}
	swapped, err := store.CompareAndSwap(ctx, "session", []byte("value"), []byte("new"), time.Hour)
	if err != nil || swapped {
		t.Fatal("比较交换不能复活已到期条目")
	}
}

func TestFileStoreRejectsLegacyFileAndFailedPersistence(t *testing.T) {
	ctx := t.Context()
	directory := t.TempDir()
	legacyPath := filepath.Join(directory, "legacy.dat")
	legacy := []byte(`{"oldKey":{"userKey":"user"}}`)
	err := os.WriteFile(legacyPath, legacy, 0600)
	if err != nil {
		t.Fatal(err)
	}
	_, err = NewFileStore(legacyPath)
	if !errors.Is(err, ErrInvalidConfig) {
		t.Fatal("不应自动导入旧 .dat 格式")
	}
	after, err := os.ReadFile(legacyPath)
	if err != nil || !bytes.Equal(after, legacy) {
		t.Fatal("不应改写旧格式文件")
	}
	path := filepath.Join(directory, "sessions.json")
	store, err := NewFileStore(path)
	if err != nil {
		t.Fatal(err)
	}
	err = store.Set(ctx, "key", []byte("old"), time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	// 用目录阻止原子替换，验证磁盘写入失败不会改变内存会话。
	err = os.Remove(path)
	if err != nil {
		t.Fatal(err)
	}
	err = os.Mkdir(path, 0700)
	if err != nil {
		t.Fatal(err)
	}
	err = store.Set(ctx, "key", []byte("new"), time.Hour)
	if err == nil {
		t.Fatal("快照不可替换时应返回错误")
	}
	value, found, err := store.Get(ctx, "key")
	if err != nil || !found || string(value) != "old" {
		t.Fatal("失败写入改变了内存会话")
	}
	ctx, cancel := context.WithCancel(ctx)
	cancel()
	err = store.Set(ctx, "key", nil, 0)
	if !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestFileStorePersistsOnlyOwnEntries(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sessions.json")
	store, err := NewFileStore(path)
	if err != nil {
		t.Fatal(err)
	}
	manager := testToken(t, store, false)
	issued, err := manager.GenToken(t.Context(), "user", json.RawMessage(`null`))
	if err != nil {
		t.Fatal(err)
	}
	reopened, err := NewFileStore(path)
	if err != nil {
		t.Fatal(err)
	}
	manager = testToken(t, reopened, false)
	session, err := manager.ValidToken(t.Context(), issued.Token)
	if err != nil || string(session.Data) != "null" {
		t.Fatal("文件存储下的令牌或 JSON 空值未能恢复")
	}
	if len(reopened.entries) != 1 {
		t.Fatal("快照应仅包含本实例写入的会话")
	}
}
