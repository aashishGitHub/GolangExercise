//go:build integration

// Run: docker compose up -d redis && go test -tags=integration ./internal/holdlock/...
package holdlock

import (
	"context"
	"testing"
	"time"
)

func newTestLock(t *testing.T) *Lock {
	t.Helper()
	l := New("localhost:6379")
	t.Cleanup(func() { _ = l.Close() })
	return l
}

func TestAcquire_FirstWinsSecondLoses(t *testing.T) {
	l := newTestLock(t)
	ctx := context.Background()
	eventID, seatID := int64(9001), int64(1)
	t.Cleanup(func() { _ = l.Release(ctx, eventID, seatID, "a") })

	ok1, err := l.Acquire(ctx, eventID, seatID, "holder-a", time.Minute)
	if err != nil || !ok1 {
		t.Fatalf("first Acquire: ok=%v err=%v, want ok=true err=nil", ok1, err)
	}

	ok2, err := l.Acquire(ctx, eventID, seatID, "holder-b", time.Minute)
	if err != nil {
		t.Fatalf("second Acquire: unexpected err=%v", err)
	}
	if ok2 {
		t.Fatal("second Acquire: ok=true, want false (key already held)")
	}
}

func TestRelease_OnlyCurrentHolderCanDelete(t *testing.T) {
	l := newTestLock(t)
	ctx := context.Background()
	eventID, seatID := int64(9001), int64(2)

	ok, err := l.Acquire(ctx, eventID, seatID, "holder-a", time.Minute)
	if err != nil || !ok {
		t.Fatalf("Acquire: ok=%v err=%v", ok, err)
	}

	// A different holder's release must be a no-op — it must NOT delete
	// holder-a's key (the compare-and-delete's whole point).
	if err := l.Release(ctx, eventID, seatID, "holder-b-not-the-owner"); err != nil {
		t.Fatalf("Release (wrong holder): unexpected err=%v", err)
	}
	stillHeld, err := l.Acquire(ctx, eventID, seatID, "holder-c", time.Minute)
	if err != nil {
		t.Fatalf("probe Acquire: unexpected err=%v", err)
	}
	if stillHeld {
		t.Fatal("key was deleted by a non-owner's Release — compare-and-delete is broken")
	}

	// The real owner's release must succeed.
	if err := l.Release(ctx, eventID, seatID, "holder-a"); err != nil {
		t.Fatalf("Release (correct holder): unexpected err=%v", err)
	}
	freed, err := l.Acquire(ctx, eventID, seatID, "holder-d", time.Minute)
	if err != nil {
		t.Fatalf("post-release Acquire: unexpected err=%v", err)
	}
	if !freed {
		t.Fatal("key still held after the correct owner released it")
	}
	_ = l.Release(ctx, eventID, seatID, "holder-d")
}

func TestAcquire_ExpiresAfterTTL(t *testing.T) {
	l := newTestLock(t)
	ctx := context.Background()
	eventID, seatID := int64(9001), int64(3)

	ok, err := l.Acquire(ctx, eventID, seatID, "holder-a", 50*time.Millisecond)
	if err != nil || !ok {
		t.Fatalf("Acquire: ok=%v err=%v", ok, err)
	}
	time.Sleep(150 * time.Millisecond)

	ok, err = l.Acquire(ctx, eventID, seatID, "holder-b", time.Minute)
	if err != nil {
		t.Fatalf("Acquire after TTL: unexpected err=%v", err)
	}
	if !ok {
		t.Fatal("key still held after its TTL expired")
	}
	_ = l.Release(ctx, eventID, seatID, "holder-b")
}

func TestAcquire_UnreachableRedisReturnsErrUnavailable(t *testing.T) {
	l := New("localhost:1") // nothing listens here
	t.Cleanup(func() { _ = l.Close() })

	_, err := l.Acquire(context.Background(), 1, 1, "x", time.Minute)
	if err == nil {
		t.Fatal("expected an error against an unreachable Redis")
	}
}
