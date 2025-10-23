package registry

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"sync"
	"time"

	"golang.org/x/sync/singleflight"
)

const (
	// defaultTTL is how long an unused pool entry stays cached.
	// If a tenant has no activity for this duration, the connection is closed.
	defaultTTL = 15 * time.Minute

	// reaperInterval is how often the background reaper sweeps for stale entries.
	reaperInterval = 5 * time.Minute

	// fetchTimeout limits how long a singleflight DSN fetch & pool open can take.
	fetchTimeout = 5 * time.Second
)

// poolEntry wraps a *sql.DB and schemaName with its last-used timestamp for TTL eviction.
type poolEntry struct {
	db         *sql.DB
	schemaName string
	lastUsed   time.Time
}

type fetchResult struct {
	db         *sql.DB
	schemaName string
}

// PoolRegistry is a thread-safe, TTL-aware cache of tenant *sql.DB pools.
//
// Design decisions:
//   - sync.RWMutex: Read-heavy path (most requests are cache hits). No contention
//     on the fast path; write lock only on first-access or eviction.
//   - singleflight.Group: Request coalescing on cache misses to prevent thundering herd /
//     cache stampede attacks against tenant-service or DB connections.
//   - Lock-free fetch: DSN fetching is executed OUTSIDE r.mu.Lock() so one slow tenant
//     fetch never blocks cache misses for other tenants.
//   - Context Shielding: DSN fetch runs under an independent context.Background() + timeout
//     so client disconnects/cancellations don't fail coalesced goroutines.
type PoolRegistry struct {
	mu      sync.RWMutex
	entries map[string]*poolEntry
	ttl     time.Duration
	sfGroup singleflight.Group
}

func NewPoolRegistry() *PoolRegistry {
	return &PoolRegistry{
		entries: make(map[string]*poolEntry),
		ttl:     defaultTTL,
	}
}

// GetOrFetch returns the cached *sql.DB and schemaName for tenantID.
// On a cache miss, it uses singleflight to request-coalesce concurrent callers,
// calls fetchDSN outside the global write lock, caches the result, and returns it. Thread-safe.
func (r *PoolRegistry) GetOrFetch(tenantID string, fetchDSN func() (*sql.DB, string, error)) (*sql.DB, string, error) {
	// 1. Fast path: read lock
	r.mu.RLock()
	entry, ok := r.entries[tenantID]
	r.mu.RUnlock()

	if ok {
		r.mu.Lock()
		entry.lastUsed = time.Now()
		r.mu.Unlock()
		return entry.db, entry.schemaName, nil
	}

	// 2. Slow path: Request coalescing with singleflight OUTSIDE the global write lock
	v, err, _ := r.sfGroup.Do(tenantID, func() (interface{}, error) {
		// Double-check cache inside singleflight worker
		r.mu.RLock()
		entry, ok := r.entries[tenantID]
		r.mu.RUnlock()

		if ok {
			r.mu.Lock()
			entry.lastUsed = time.Now()
			r.mu.Unlock()
			return fetchResult{db: entry.db, schemaName: entry.schemaName}, nil
		}

		// Execute fetchDSN lock-free.
		// fetchDSN uses an independent context to avoid caller context cancellation cascading to coalesced waiters.
		db, schemaName, err := fetchDSN()
		if err != nil {
			return nil, err
		}

		// Store in pool entries under write lock
		r.mu.Lock()
		r.entries[tenantID] = &poolEntry{db: db, schemaName: schemaName, lastUsed: time.Now()}
		r.mu.Unlock()

		log.Printf("PoolRegistry: Opened & cached new pool for tenant '%s' (schema: '%s')", tenantID, schemaName)
		return fetchResult{db: db, schemaName: schemaName}, nil
	})

	if err != nil {
		return nil, "", fmt.Errorf("pool registry: failed to open pool for tenant '%s': %w", tenantID, err)
	}

	res := v.(fetchResult)
	return res.db, res.schemaName, nil
}

// Evict closes and removes the pool for a specific tenantID.
// Used when tenant-service emits a TenantInfrastructureChanged event (plan upgrade).
func (r *PoolRegistry) Evict(tenantID string) {
	r.mu.Lock()
	entry, ok := r.entries[tenantID]
	if ok {
		delete(r.entries, tenantID)
	}
	r.mu.Unlock()

	r.sfGroup.Forget(tenantID)

	if !ok {
		return
	}
	if err := entry.db.Close(); err != nil {
		log.Printf("PoolRegistry: Error closing pool for tenant '%s' during eviction: %v", tenantID, err)
	}
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
