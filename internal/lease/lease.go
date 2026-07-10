// Package lease implements machine leases. A lease grants its holder exclusive
// rights to mutate a machine: the holder proves ownership by echoing the lease
// nonce in the fly-machine-lease-nonce header. Leases carry a TTL and expire on
// the injected clock, so expiry is deterministic in tests.
package lease

import (
	"errors"
	"sync"
	"time"

	"github.com/intentius/mudflaps/internal/clock"
	"github.com/intentius/mudflaps/internal/id"
)

// Errors returned by the manager.
var (
	// ErrConflict means a lease is held by someone else (a 409 to the client).
	ErrConflict = errors.New("lease currently held")
	// ErrNotFound means no active lease exists for the key.
	ErrNotFound = errors.New("no active lease")
	// ErrNonceMismatch means the supplied nonce does not match the held lease.
	ErrNonceMismatch = errors.New("lease nonce mismatch")
)

// DefaultTTL is used when a caller requests a lease without a TTL.
const DefaultTTL = 30 * time.Second

// Lease is an active hold on a machine.
type Lease struct {
	Nonce       string
	Owner       string
	Description string
	ExpiresAt   time.Time
}

// Manager tracks leases keyed by an opaque string (mudflaps uses "app/machine").
type Manager struct {
	clk clock.Clock
	mu  sync.Mutex
	m   map[string]*Lease
}

// New returns a lease manager driven by clk.
func New(clk clock.Clock) *Manager {
	return &Manager{clk: clk, m: make(map[string]*Lease)}
}

// Acquire takes a new lease for key. If an unexpired lease is already held it
// returns ErrConflict along with that lease so the caller can report it.
func (mgr *Manager) Acquire(key, owner, description string, ttl time.Duration) (*Lease, error) {
	if ttl <= 0 {
		ttl = DefaultTTL
	}
	mgr.mu.Lock()
	defer mgr.mu.Unlock()
	now := mgr.clk.Now()
	if existing := mgr.m[key]; existing != nil && existing.ExpiresAt.After(now) {
		return copyLease(existing), ErrConflict
	}
	l := &Lease{
		Nonce:       id.Nonce(),
		Owner:       owner,
		Description: description,
		ExpiresAt:   now.Add(ttl),
	}
	mgr.m[key] = l
	return copyLease(l), nil
}

// Get returns the active lease for key, or ErrNotFound if none is held or the
// held one has expired.
func (mgr *Manager) Get(key string) (*Lease, error) {
	mgr.mu.Lock()
	defer mgr.mu.Unlock()
	l := mgr.active(key)
	if l == nil {
		return nil, ErrNotFound
	}
	return copyLease(l), nil
}

// Refresh extends an existing lease. The caller must present the matching
// nonce.
func (mgr *Manager) Refresh(key, nonce string, ttl time.Duration) (*Lease, error) {
	if ttl <= 0 {
		ttl = DefaultTTL
	}
	mgr.mu.Lock()
	defer mgr.mu.Unlock()
	l := mgr.active(key)
	if l == nil {
		return nil, ErrNotFound
	}
	if l.Nonce != nonce {
		return nil, ErrNonceMismatch
	}
	l.ExpiresAt = mgr.clk.Now().Add(ttl)
	return copyLease(l), nil
}

// Release drops a lease. The caller must present the matching nonce.
func (mgr *Manager) Release(key, nonce string) error {
	mgr.mu.Lock()
	defer mgr.mu.Unlock()
	l := mgr.active(key)
	if l == nil {
		return ErrNotFound
	}
	if l.Nonce != nonce {
		return ErrNonceMismatch
	}
	delete(mgr.m, key)
	return nil
}

// Check enforces lease ownership for a mutating operation. If an active lease
// is held and nonce does not match it, Check returns ErrConflict; otherwise it
// returns nil (including when no lease is held at all).
func (mgr *Manager) Check(key, nonce string) error {
	mgr.mu.Lock()
	defer mgr.mu.Unlock()
	l := mgr.active(key)
	if l == nil {
		return nil
	}
	if l.Nonce != nonce {
		return ErrConflict
	}
	return nil
}

// Clear removes any lease for key regardless of nonce. It is used when a machine
// is destroyed.
func (mgr *Manager) Clear(key string) {
	mgr.mu.Lock()
	defer mgr.mu.Unlock()
	delete(mgr.m, key)
}

// active returns the live lease for key, deleting it if it has expired. Callers
// must hold mgr.mu.
func (mgr *Manager) active(key string) *Lease {
	l := mgr.m[key]
	if l == nil {
		return nil
	}
	if !l.ExpiresAt.After(mgr.clk.Now()) {
		delete(mgr.m, key)
		return nil
	}
	return l
}

func copyLease(l *Lease) *Lease {
	c := *l
	return &c
}
