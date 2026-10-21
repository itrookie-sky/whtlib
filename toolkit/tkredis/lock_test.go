package tkredis

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

func waitLockIdle(t *testing.T, lock *Lock) {
	t.Helper()
	timeout := time.NewTimer(2 * time.Second)
	defer timeout.Stop()
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	for {
		lock.mu.Lock()
		idle := lock.lease == nil
		lock.mu.Unlock()
		if idle {
			return
		}
		select {
		case <-timeout.C:
			t.Fatal("lock maintenance did not stop")
		case <-ticker.C:
		}
	}
}

func TestLockRenewUnlockAndReacquire(t *testing.T) {
	client, server := newTestClient(t)
	ctx := t.Context()
	lock, err := client.NewLock("lease", WithExpiration(90*time.Millisecond))
	if err != nil {
		t.Fatal(err)
	}
	ok, err := lock.TryLock(ctx)
	if err != nil || !ok {
		t.Fatalf("acquire = %v, %v", ok, err)
	}
	firstOwner, err := server.Get("lease")
	if err != nil || len(firstOwner) != 48 || server.TTL("lease") != 90*time.Millisecond {
		t.Fatalf("owner=%q, ttl=%v, error=%v", firstOwner, server.TTL("lease"), err)
	}
	_, err = lock.TryLock(ctx)
	if !errors.Is(err, ErrLockHeld) {
		t.Fatalf("same instance acquire error = %v", err)
	}
	other, err := client.NewLock("lease", WithExpiration(90*time.Millisecond))
	if err != nil {
		t.Fatal(err)
	}
	ok, err = other.TryLock(ctx)
	if err != nil || ok {
		t.Fatalf("contending acquire = %v, %v", ok, err)
	}
	server.FastForward(70 * time.Millisecond)
	// miniredis 的时间由 FastForward 驱动，此处等待一次真实续期周期。
	timeout := time.NewTimer(time.Second)
	defer timeout.Stop()
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	for server.TTL("lease") < 80*time.Millisecond {
		select {
		case <-timeout.C:
			t.Fatal("lease was not renewed with millisecond TTL")
		case <-ticker.C:
		}
	}
	ok, err = lock.Unlock(ctx)
	if err != nil || !ok || server.Exists("lease") {
		t.Fatalf("unlock=%v, error=%v, exists=%v", ok, err, server.Exists("lease"))
	}
	ok, err = lock.TryLock(ctx)
	if err != nil || !ok {
		t.Fatalf("reacquire=%v, error=%v", ok, err)
	}
	secondOwner, err := server.Get("lease")
	if err != nil || firstOwner == secondOwner {
		t.Fatalf("owner must change: error=%v", err)
	}
	ok, err = lock.Unlock(ctx)
	if err != nil || !ok {
		t.Fatalf("second unlock=%v, error=%v", ok, err)
	}
}

func TestLockNeverRenewsOrDeletesAnotherOwner(t *testing.T) {
	client, server := newTestClient(t)
	lock, err := client.NewLock("foreign", WithExpiration(60*time.Millisecond))
	if err != nil {
		t.Fatal(err)
	}
	ok, err := lock.TryLock(t.Context())
	if err != nil || !ok {
		t.Fatalf("acquire=%v, error=%v", ok, err)
	}
	err = server.Set("foreign", "new-owner")
	if err != nil {
		t.Fatal(err)
	}
	server.SetTTL("foreign", 123*time.Millisecond)
	waitLockIdle(t, lock)
	owner, err := server.Get("foreign")
	if err != nil || owner != "new-owner" || server.TTL("foreign") != 123*time.Millisecond {
		t.Fatalf("foreign owner=%q, ttl=%v, error=%v", owner, server.TTL("foreign"), err)
	}
	ok, err = lock.Unlock(t.Context())
	if err != nil || ok || !server.Exists("foreign") {
		t.Fatalf("foreign unlock=%v, error=%v", ok, err)
	}
}

func TestLockCancellationAndClientCloseReleaseLeases(t *testing.T) {
	client, server := newTestClient(t)
	ctx, cancel := context.WithCancel(t.Context())
	lock, err := client.NewLock("canceled", WithExpiration(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	ok, err := lock.TryLock(ctx)
	if err != nil || !ok {
		t.Fatalf("acquire=%v, error=%v", ok, err)
	}
	cancel()
	waitLockIdle(t, lock)
	if server.Exists("canceled") {
		t.Fatal("ctx cancellation left a live lease")
	}
	ok, err = lock.TryLock(t.Context())
	if err != nil || !ok {
		t.Fatalf("acquire after cancel=%v, error=%v", ok, err)
	}
	var group sync.WaitGroup
	for range 5 {
		group.Go(func() {
			err := client.Close()
			if err != nil {
				t.Error(err)
			}
		})
	}
	group.Wait()
	if server.Exists("canceled") {
		t.Fatal("Close returned before releasing lease")
	}
	_, err = lock.TryLock(t.Context())
	if !errors.Is(err, ErrClosed) {
		t.Fatalf("closed lock error=%v", err)
	}
}

func TestLockSpinRespondsToCancellation(t *testing.T) {
	client, server := newTestClient(t)
	err := server.Set("busy", "other-owner")
	if err != nil {
		t.Fatal(err)
	}
	lock, err := client.NewLock("busy", WithRetryInterval(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer cancel()
	started := time.Now()
	ok, err := lock.SpinLock(ctx, 100)
	if !errors.Is(err, context.DeadlineExceeded) || ok || time.Since(started) > 500*time.Millisecond {
		t.Fatalf("canceled spin=%v, error=%v, elapsed=%v", ok, err, time.Since(started))
	}
}
