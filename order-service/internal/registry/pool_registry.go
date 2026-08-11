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

	// defaultTTL is how long an unused pool entry stays cached (tuned down to 3 mins to aggressively release sockets).
	defaultTTL = 3 * time.Minute

	// reaperInterval is how often the background reaper sweeps for stale entries.
	reaperInterval = 1 * time.Minute

	// fetchTimeout limits how long a singleflight DSN fetch & pool open can take.
	fetchTimeout = 5 * time.Second

	// cleanupQueueCapacity is the buffer depth for the backgroundSweeper channel.
	// Absorbs up to 5 rapid PurgeAll() calls before the overflow goroutine path is taken.
	cleanupQueueCapacity = 5
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

func WithMaxCapacity(capacity int) PoolRegistryOption {
	return func(pr *PoolRegistry) {
		if capacity > 0 {
			pr.maxCapacity = capacity
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

func WithFetchTimeout(timeout time.Duration) PoolRegistryOption {
	return func(pr *PoolRegistry) {
		if timeout > 0 {
			pr.fetchTimeout = timeout
		}
	}
}

// PoolRegistry is a thread-safe, Bounded LRU cache of tenant *sql.DB pools.
// A single backgroundSweeper goroutine owns all async pool cleanup — preventing
// the goroutine leak that would result from spawning inline goroutines per eviction.
type PoolRegistry struct {
	mu           sync.RWMutex
	entries      map[string]*poolEntry
	maxCapacity  int
	ttl          time.Duration
	fetchTimeout time.Duration
	sfGroup      singleflight.Group

	// cleanupQueue receives maps of stale pool entries from PurgeAll().
	// The backgroundSweeper goroutine is the sole consumer.
	// Bounded at cleanupQueueCapacity to prevent unbounded memory growth on broker flap.
	cleanupQueue chan map[string]*poolEntry
}

func NewPoolRegistry(opts ...PoolRegistryOption) *PoolRegistry {
	pr := &PoolRegistry{
		entries:      make(map[string]*poolEntry),
		maxCapacity:  defaultMaxDedicatedCapacity,
		ttl:          defaultTTL,
		fetchTimeout: fetchTimeout,
		cleanupQueue: make(chan map[string]*poolEntry, cleanupQueueCapacity),
	}
	for _, opt := range opts {
		opt(pr)
	}
	// Start the single long-lived sweeper goroutine.
	// Explicit goroutine ownership: this goroutine is the only one that closes *sql.DB handles
	// from PurgeAll(), and it terminates when cleanupQueue is closed (on closeAll()).
	go pr.backgroundSweeper()
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
	// Uses DoChan with a fetchTimeout context barrier to prevent hanging leaders from locking callers indefinitely.
	ctx, cancel := context.WithTimeout(context.Background(), r.fetchTimeout)
	defer cancel()

	ch := r.sfGroup.DoChan(tenantID, func() (interface{}, error) {
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

		// Ensure strict connection idle settings to prevent host connection & socket leaks
		db.SetMaxIdleConns(1)
		db.SetMaxOpenConns(3)
		db.SetConnMaxIdleTime(30 * time.Second)
		db.SetConnMaxLifetime(5 * time.Minute)

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

	select {
	case res := <-ch:
		if res.Err != nil {
			return nil, "", fmt.Errorf("pool registry: failed to open pool for tenant '%s': %w", tenantID, res.Err)
		}
		resVal := res.Val.(fetchResult)
		return resVal.db, resVal.schemaName, nil
	case <-ctx.Done():
		r.sfGroup.Forget(tenantID)
		return nil, "", fmt.Errorf("pool registry: singleflight leader timed out after %v opening pool for tenant '%s': %w", r.fetchTimeout, tenantID, ctx.Err())
	}
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

// PurgeAll evicts every tenant pool from the registry in O(1) time and schedules
// their graceful closure without holding the global write lock.
//
// Mechanism:
//  1. Swap the live entries map for a new empty map under the write lock — O(1), minimal lock hold time.
//  2. Hand the stale map to backgroundSweeper via cleanupQueue (non-blocking).
//  3. If cleanupQueue is full (extreme broker flapping), spawn an overflow goroutine.
//     The overflow goroutine violates strict bounded-goroutine policy, but this is the
//     lesser evil: dropping *sql.DB pointers without Close() leaves TCP sockets in
//     ESTABLISHED/CLOSE_WAIT, eventually causing "too many open files".
func (r *PoolRegistry) PurgeAll() {
	r.mu.Lock()
	staleEntries := r.entries
	r.entries = make(map[string]*poolEntry) // O(1) pointer swap — lock released immediately after
	r.mu.Unlock()

	count := len(staleEntries)
	if count == 0 {
		return
	}

	// Primary path: hand stale map to the backgroundSweeper non-blocking.
	select {
	case r.cleanupQueue <- staleEntries:
		log.Printf("PoolRegistry: PurgeAll queued cleanup of %d pools", count)
	default:
		// The sweeper is overwhelmed (extreme broker flapping, queue at capacity).
		// We CANNOT drop this map: the Go GC does not call db.Close() when *sql.DB
		// pointers become unreachable. Underlying TCP sockets persist in ESTABLISHED or
		// CLOSE_WAIT until the database server force-kills them or the OS exhausts
		// ephemeral ports with "too many open files".
		// Spawn an overflow goroutine as the lesser evil to guarantee FD reclamation.
		go func(m map[string]*poolEntry) {
			log.Printf("WARN: PoolRegistry cleanup queue full; spawning overflow worker to prevent FD leak (%d pools)", len(m))
			for tenantID, entry := range m {
				r.sfGroup.Forget(tenantID)
				r.closePoolGracefully(entry.db, tenantID)
			}
		}(staleEntries)
	}
}

// backgroundSweeper is the single long-lived goroutine that drains the cleanupQueue.
// It terminates when cleanupQueue is closed (triggered by closeAll() on graceful shutdown).
func (r *PoolRegistry) backgroundSweeper() {
	for stale := range r.cleanupQueue {
		for tenantID, entry := range stale {
			r.sfGroup.Forget(tenantID)
			r.closePoolGracefully(entry.db, tenantID)
		}
		log.Printf("PoolRegistry: backgroundSweeper drained a cleanup batch of %d pools", len(stale))
	}
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
	cutoff := time.Now().Add(-r.ttl)

	// Step 1: Collect candidates under RLock
	r.mu.RLock()
	var candidates []string
	for tenantID, entry := range r.entries {
		if entry.lastUsed.Before(cutoff) {
			candidates = append(candidates, tenantID)
		}
	}
	r.mu.RUnlock()

	if len(candidates) == 0 {
		return
	}

	// Step 2: Per-candidate double-check under Write Lock & graceful close outside lock
	for _, tenantID := range candidates {
		var dbToClose *sql.DB

		r.mu.Lock()
		entry, ok := r.entries[tenantID]
		// Double-check: ensure entry still exists and lastUsed is STILL before cutoff
		if ok && entry.lastUsed.Before(cutoff) {
			delete(r.entries, tenantID)
			dbToClose = entry.db
		}
		r.mu.Unlock()

		if dbToClose != nil {
			r.closePoolGracefully(dbToClose, tenantID)
			log.Printf("PoolRegistry Reaper: Evicted idle pool for tenant '%s' (double-checked)", tenantID)
		}
	}
}

// closeAll drains and closes all remaining pools on graceful shutdown.
// Closing cleanupQueue terminates the backgroundSweeper goroutine cleanly.
func (r *PoolRegistry) closeAll() {
	r.mu.Lock()
	remaining := r.entries
	r.entries = make(map[string]*poolEntry)
	r.mu.Unlock()

	for tenantID, entry := range remaining {
		r.closePoolGracefully(entry.db, tenantID)
		delete(remaining, tenantID)
	}

	// Signal the backgroundSweeper to exit after draining any queued batches.
	close(r.cleanupQueue)
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
