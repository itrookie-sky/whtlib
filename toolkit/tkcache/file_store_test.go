package tkcache

import (
	"bytes"
	"context"
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
	store, err := openTestFileCache(t, path)
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
	expiry := store.store.(*fileStore).memory.entries[store.prefix+"data:session"].expires
	_ = store.Close()
	reopened, err := openTestFileCache(t, path)
	if err != nil {
		t.Fatal(err)
	}
	if !reopened.store.(*fileStore).memory.entries[reopened.prefix+"data:session"].expires.Equal(expiry) {
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
	_ = reopened.Close()
	store, err = openTestFileCache(t, path)
	if err != nil {
		t.Fatal(err)
	}
	past := time.Now().Add(-2 * time.Hour)
	store.store.(*fileStore).memory.now = func() (value time.Time) { return past }
	err = store.Set(ctx, "expired", []byte("data"), time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	_ = store.Close()
	reopened, err = openTestFileCache(t, path)
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
	store, err := openTestFileCache(t, filepath.Join(t.TempDir(), "sessions.json"))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	store.store.(*fileStore).memory.now = func() (value time.Time) { return now }
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
	_, err = openTestFileCache(t, legacyPath)
	if !errors.Is(err, ErrInvalidConfig) {
		t.Fatal("不应自动导入旧 .dat 格式")
	}
	after, err := os.ReadFile(legacyPath)
	if err != nil || !bytes.Equal(after, legacy) {
		t.Fatal("不应改写旧格式文件")
	}
	path := filepath.Join(directory, "sessions.json")
	store, err := openTestFileCache(t, path)
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

	// 删除、续期和清空也必须等待持久化成功，失败不能报告已修改。
	deleted, err := store.CompareAndDelete(ctx, "key", []byte("old"))
	if err == nil || deleted {
		t.Fatal("删除失败不应报告成功")
	}
	_, found, err = store.GetAndExpire(ctx, "key", 0)
	if err == nil || found {
		t.Fatal("续期失败不应返回成功结果")
	}
	ttl, exists, err := store.TTL(ctx, "key")
	if err != nil || !exists || ttl <= 0 {
		t.Fatal("续期失败改变了 TTL")
	}
	err = store.Clear(ctx)
	if err == nil {
		t.Fatal("清空持久化失败应报错")
	}
	wantValue(t, store, "key", "old", true)
	temporary, err := filepath.Glob(filepath.Join(directory, ".tkcache-*.tmp"))
	if err != nil || len(temporary) != 0 {
		t.Fatal("失败写入遗留临时文件")
	}
	ctx, cancel := context.WithCancel(ctx)
	cancel()
	err = store.Set(ctx, "key", nil, 0)
	if !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func openTestFileCache(t *testing.T, path string) (cache *Cache, err error) {
	t.Helper()
	cache, err = New(Config{Store: FileStore, Namespace: "file_test", FilePath: path})
	if err == nil {
		t.Cleanup(func() { _ = cache.Close() })
	}
	return
}

func TestFileStoreNamespaceAndClearSurviveRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cache.json")
	open := func(namespace string) (cache *Cache) {
		t.Helper()
		var err error
		cache, err = New(Config{Store: FileStore, Namespace: namespace, FilePath: path})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = cache.Close() })
		return
	}
	first := open("first")
	mustSet(t, first, "key", "first", NoExpiration)
	ok, err := first.Once(t.Context(), "key", time.Hour)
	if err != nil || !ok {
		t.Fatal("占位失败", err)
	}
	_ = first.Close()
	second := open("second")
	wantValue(t, second, "key", "", false)
	mustSet(t, second, "key", "second", NoExpiration)
	_ = second.Close()
	first = open("first")
	wantValue(t, first, "key", "first", true)
	ok, err = first.Once(t.Context(), "key", time.Hour)
	if err != nil || ok {
		t.Fatal("重启丢失占位", err)
	}
	err = first.Clear(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	_ = first.Close()
	first = open("first")
	wantValue(t, first, "key", "", false)
	ok, err = first.Once(t.Context(), "key", time.Hour)
	if err != nil || !ok {
		t.Fatal("Clear 未持久化占位删除", err)
	}
	_ = first.Close()
	second = open("second")
	wantValue(t, second, "key", "second", true)
}

func TestFileStoreRejectsInvalidPaths(t *testing.T) {
	directory := t.TempDir()
	target := filepath.Join(directory, "target.json")
	store, err := openTestFileCache(t, target)
	if err != nil {
		t.Fatal(err)
	}
	_ = store.Close()
	link := filepath.Join(directory, "link.json")
	err = os.Symlink(target, link)
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{directory, link} {
		_, err = openTestFileCache(t, path)
		if !errors.Is(err, ErrInvalidConfig) {
			t.Fatalf("应拒绝路径 %q: %v", path, err)
		}
	}
	_, err = openTestFileCache(t, filepath.Join(directory, "missing", "cache.json"))
	if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("父目录缺失应报错: %v", err)
	}
}
