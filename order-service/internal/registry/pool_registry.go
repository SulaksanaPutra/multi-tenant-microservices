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
	// defaultMaxDedicatedCapacity is the maximum number of active pools stored for Dedicated Plan tenants.
	defaultMaxDedicatedCapacity = 50

	// defaultTTL is how long an unused pool entry stays cached.
	defaultTTL = 15 * time.Minute

	// reaperInterval is how often the background reaper sweeps for stale entries.
	reaperInterval = 5 * time.Minute

	// fetchTimeout limits how long a singleflight DSN fetch & pool open can take.
	fetchTimeout = 5 * time.Second
)

type poolEntry struct {
	db         *sql.DB
	schemaName string
	lastUsed   time.Time
}

type fetchResult struct {
	db         *sql.DB
	schemaName string
}

type PoolRegistryOption func(*PoolRegistry)

func WithMaxCapacity(cap int) PoolRegistryOption {
	return func(pr *PoolRegistry) {
		if cap > 0 {
			pr.maxCapacity = cap
		}
	}
}

func WithTTL(ttl time.Duration) PoolRegistryOption {
	return func(pr *PoolRegistry) {
		if ttl > 0 {
			pr.ttl = ttl
		}
	}
}

// PoolRegistry is a thread-safe, Bounded LRU cache of tenant *sql.DB pools.
type PoolRegistry struct {
	mu          sync.RWMutex
	entries     map[string]*poolEntry
	maxCapacity int
	ttl         time.Duration
	sfGroup     singleflight.Group
}

func NewPoolRegistry(opts ...PoolRegistryOption) *PoolRegistry {
	pr := &PoolRegistry{
		entries:     make(map[string]*poolEntry),
		maxCapacity: defaultMaxDedicatedCapacity,
		ttl:         defaultTTL,
	}
	for _, opt := range opts {
		opt(pr)
	}
	return pr
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
		r.mu.RLock()
		entry, ok := r.entries[tenantID]
		r.mu.RUnlock()

		if ok {
			r.mu.Lock()
			entry.lastUsed = time.Now()
			r.mu.Unlock()
			return fetchResult{db: entry.db, schemaName: entry.schemaName}, nil
		}

		db, schemaName, err := fetchDSN()
		if err != nil {
			return nil, err
		}

		// Ensure connection idle settings to prevent connection leaks
		db.SetMaxIdleConns(2)
		db.SetMaxOpenConns(10)
		db.SetConnMaxIdleTime(1 * time.Minute)
		db.SetConnMaxLifetime(15 * time.Minute)

		r.mu.Lock()
		// Evict LRU entry if maxCapacity is reached
		if len(r.entries) >= r.maxCapacity {
			r.evictLRULocked()
		}

		r.entries[tenantID] = &poolEntry{db: db, schemaName: schemaName, lastUsed: time.Now()}
		r.mu.Unlock()

		log.Printf("PoolRegistry: Opened & cached dedicated pool for tenant '%s' (schema: '%s')", tenantID, schemaName)
		return fetchResult{db: db, schemaName: schemaName}, nil
	})

	if err != nil {
		return nil, "", fmt.Errorf("pool registry: failed to open pool for tenant '%s': %w", tenantID, err)
	}

	res := v.(fetchResult)
	return res.db, res.schemaName, nil
}

// evictLRULocked evicts the least recently used pool entry. Must be called with r.mu write-lock held.
func (r *PoolRegistry) evictLRULocked() {
	var oldestTenant string
	var oldestTime time.Time
	first := true

	for tID, entry := range r.entries {
		if first || entry.lastUsed.Before(oldestTime) {
			oldestTime = entry.lastUsed
			oldestTenant = tID
			first = false
		}
	}

	if oldestTenant != "" {
		entry := r.entries[oldestTenant]
		delete(r.entries, oldestTenant)
		log.Printf("PoolRegistry: LRU capacity reached (%d). Evicting tenant '%s'", r.maxCapacity, oldestTenant)
		r.closePoolGracefully(entry.db, oldestTenant)
	}
}

// Evict closes and removes the pool for a specific tenantID.
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
	r.closePoolGracefully(entry.db, tenantID)
	log.Printf("PoolRegistry: Evicted dedicated pool for tenant '%s'", tenantID)
}

// StartReaper launches a background goroutine that periodically closes idle pools.
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
	cutoff := time.Now().Add(-r.ttl)
	for tenantID, entry := range r.entries {
		if entry.lastUsed.Before(cutoff) {
			delete(r.entries, tenantID)
			r.closePoolGracefully(entry.db, tenantID)
			log.Printf("PoolRegistry Reaper: Evicted idle pool for tenant '%s'", tenantID)
		}
	}
	r.mu.Unlock()
}

func (r *PoolRegistry) closeAll() {
	r.mu.Lock()
	defer r.mu.Unlock()

	for tenantID, entry := range r.entries {
		r.closePoolGracefully(entry.db, tenantID)
		delete(r.entries, tenantID)
	}
	log.Printf("PoolRegistry: All pools scheduled for graceful close.")
}

// closePoolGracefully drains active connections before calling db.Close() to prevent breaking in-flight queries.
func (r *PoolRegistry) closePoolGracefully(db *sql.DB, tenantID string) {
	go func() {
		ticker := time.NewTicker(100 * time.Millisecond)
		defer ticker.Stop()
		timeout := time.After(30 * time.Second)

		for {
			select {
			case <-timeout:
				if err := db.Close(); err != nil {
					log.Printf("PoolRegistry: Timeout error closing pool for tenant '%s': %v", tenantID, err)
				}
				return
			case <-ticker.C:
				if db.Stats().InUse == 0 {
					if err := db.Close(); err != nil {
						log.Printf("PoolRegistry: Error closing drained pool for tenant '%s': %v", tenantID, err)
					}
					return
				}
			}
		}
	}()
}

