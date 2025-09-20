package registry

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"sync"
	"time"
)

const (
	// defaultTTL is how long an unused pool entry stays cached.
	// If a tenant has no activity for this duration, the connection is closed.
	defaultTTL = 15 * time.Minute

	// reaperInterval is how often the background reaper sweeps for stale entries.
	reaperInterval = 5 * time.Minute
)

// poolEntry wraps a *sql.DB with its last-used timestamp for TTL eviction.
type poolEntry struct {
	db       *sql.DB
	lastUsed time.Time
}

// PoolRegistry is a thread-safe, TTL-aware cache of tenant *sql.DB pools.
//
// Design decisions:
//   - sync.RWMutex: Read-heavy path (most requests are cache hits). No contention
//     on the fast path; write lock only on first-access or eviction.
//   - TTL eviction: Prevents unbounded connection growth across restarts and upgrades.
//     A 15-min idle timeout means a 5-replica * 50-tenant setup at peak usage still
//     sheds pools for tenants that go quiet.
//   - Per-pool limits enforced at creation time (see postgres.NewClientFromDSN).
type PoolRegistry struct {
	mu      sync.RWMutex
	entries map[string]*poolEntry
	ttl     time.Duration
}

func NewPoolRegistry() *PoolRegistry {
	return &PoolRegistry{
		entries: make(map[string]*poolEntry),
		ttl:     defaultTTL,
	}
}

// GetOrFetch returns the cached *sql.DB for tenantID.
// On a cache miss, it calls fetchDSN to get the DSN, opens a new pool,
// caches it, and returns it. Thread-safe.
func (r *PoolRegistry) GetOrFetch(tenantID string, fetchDSN func() (*sql.DB, error)) (*sql.DB, error) {
	// Fast path: read lock
	r.mu.RLock()
	entry, ok := r.entries[tenantID]
	r.mu.RUnlock()

	if ok {
		r.mu.Lock()
		entry.lastUsed = time.Now()
		r.mu.Unlock()
		return entry.db, nil
	}

	// Slow path: write lock, double-check, then open
	r.mu.Lock()
	defer r.mu.Unlock()

	// Double-check after acquiring write lock — another goroutine may have populated it
	if entry, ok = r.entries[tenantID]; ok {
		entry.lastUsed = time.Now()
		return entry.db, nil
	}

	db, err := fetchDSN()
	if err != nil {
		return nil, fmt.Errorf("pool registry: failed to open pool for tenant '%s': %w", tenantID, err)
	}

	r.entries[tenantID] = &poolEntry{db: db, lastUsed: time.Now()}
	log.Printf("PoolRegistry: Opened new pool for tenant '%s'", tenantID)
	return db, nil
}

// Evict closes and removes the pool for a specific tenantID.
// Used when tenant-service emits a TenantInfrastructureChanged event (plan upgrade).
func (r *PoolRegistry) Evict(tenantID string) {
	r.mu.Lock()
	defer r.mu.Unlock()

	entry, ok := r.entries[tenantID]
	if !ok {
		return
	}
	if err := entry.db.Close(); err != nil {
		log.Printf("PoolRegistry: Error closing pool for tenant '%s' during eviction: %v", tenantID, err)
	}
	delete(r.entries, tenantID)
	log.Printf("PoolRegistry: Evicted pool for tenant '%s'", tenantID)
}

// StartReaper launches a background goroutine that periodically closes idle pools.
// It should be started once at service startup and respects context cancellation.
func (r *PoolRegistry) StartReaper(ctx context.Context) {
	go func() {
		ticker := time.NewTicker(reaperInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				r.closeAll()
				return
			case <-ticker.C:
				r.reap()
			}
		}
	}()
}

func (r *PoolRegistry) reap() {
	r.mu.Lock()
	defer r.mu.Unlock()

	cutoff := time.Now().Add(-r.ttl)
	for tenantID, entry := range r.entries {
		if entry.lastUsed.Before(cutoff) {
			if err := entry.db.Close(); err != nil {
				log.Printf("PoolRegistry Reaper: Error closing pool for tenant '%s': %v", tenantID, err)
			}
			delete(r.entries, tenantID)
			log.Printf("PoolRegistry Reaper: Evicted idle pool for tenant '%s' (last used: %v)", tenantID, entry.lastUsed)
		}
	}
}

func (r *PoolRegistry) closeAll() {
	r.mu.Lock()
	defer r.mu.Unlock()

	for tenantID, entry := range r.entries {
		if err := entry.db.Close(); err != nil {
			log.Printf("PoolRegistry: Error closing pool for tenant '%s' on shutdown: %v", tenantID, err)
		}
		delete(r.entries, tenantID)
	}
	log.Printf("PoolRegistry: All pools closed.")
}
