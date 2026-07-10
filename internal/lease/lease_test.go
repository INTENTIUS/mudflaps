package lease

import (
	"testing"
	"time"

	"github.com/intentius/mudflaps/internal/clock"
)

func TestAcquireAndConflict(t *testing.T) {
	clk := clock.NewFake(time.Time{})
	mgr := New(clk)

	l, err := mgr.Acquire("app/m1", "owner", "deploy", 10*time.Second)
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	if l.Nonce == "" {
		t.Fatal("expected a nonce")
	}

	// A second acquire while held must conflict.
	if _, err := mgr.Acquire("app/m1", "other", "", 10*time.Second); err != ErrConflict {
		t.Fatalf("second Acquire = %v, want ErrConflict", err)
	}

	// Check rejects a non-holder and admits the holder.
	if err := mgr.Check("app/m1", "wrong-nonce"); err != ErrConflict {
		t.Fatalf("Check(wrong) = %v, want ErrConflict", err)
	}
	if err := mgr.Check("app/m1", l.Nonce); err != nil {
		t.Fatalf("Check(holder) = %v, want nil", err)
	}
	// An unleased key admits anyone.
	if err := mgr.Check("app/other", ""); err != nil {
		t.Fatalf("Check(unleased) = %v, want nil", err)
	}
}

func TestExpiry(t *testing.T) {
	clk := clock.NewFake(time.Time{})
	mgr := New(clk)

	l, err := mgr.Acquire("app/m1", "owner", "", 5*time.Second)
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}

	// Before expiry the lease still blocks others.
	clk.Advance(4 * time.Second)
	if _, err := mgr.Acquire("app/m1", "other", "", time.Second); err != ErrConflict {
		t.Fatalf("Acquire before expiry = %v, want ErrConflict", err)
	}

	// After expiry a fresh acquire succeeds with a new nonce.
	clk.Advance(2 * time.Second)
	l2, err := mgr.Acquire("app/m1", "other", "", 5*time.Second)
	if err != nil {
		t.Fatalf("Acquire after expiry: %v", err)
	}
	if l2.Nonce == l.Nonce {
		t.Fatal("expected a fresh nonce after expiry")
	}
	if _, err := mgr.Get("app/m1"); err != nil {
		t.Fatalf("Get after re-acquire: %v", err)
	}
}

func TestRefreshAndRelease(t *testing.T) {
	clk := clock.NewFake(time.Time{})
	mgr := New(clk)

	l, err := mgr.Acquire("app/m1", "owner", "", 5*time.Second)
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}

	if _, err := mgr.Refresh("app/m1", "wrong", 5*time.Second); err != ErrNonceMismatch {
		t.Fatalf("Refresh(wrong) = %v, want ErrNonceMismatch", err)
	}

	clk.Advance(3 * time.Second)
	if _, err := mgr.Refresh("app/m1", l.Nonce, 10*time.Second); err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	// Refresh extended the TTL, so advancing past the original expiry is fine.
	clk.Advance(4 * time.Second)
	if err := mgr.Check("app/m1", "someone"); err != ErrConflict {
		t.Fatalf("lease should still be held after refresh, got %v", err)
	}

	if err := mgr.Release("app/m1", "wrong"); err != ErrNonceMismatch {
		t.Fatalf("Release(wrong) = %v, want ErrNonceMismatch", err)
	}
	if err := mgr.Release("app/m1", l.Nonce); err != nil {
		t.Fatalf("Release: %v", err)
	}
	if _, err := mgr.Get("app/m1"); err != ErrNotFound {
		t.Fatalf("Get after release = %v, want ErrNotFound", err)
	}
}
