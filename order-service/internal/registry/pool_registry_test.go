package registry_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	_ "github.com/lib/pq"
	"order-service/internal/registry"
)

func TestPoolRegistry_Options(t *testing.T) {
	reg := registry.NewPoolRegistry(
		registry.WithMaxCapacity(10),
		registry.WithTTL(5*time.Minute),
		registry.WithFetchTimeout(2*time.Second),
	)

	if reg == nil {
		t.Fatal("expected non-nil PoolRegistry")
	}
}

func TestPoolRegistry_GetOrFetch_CacheHitAndMiss(t *testing.T) {
	reg := registry.NewPoolRegistry()

	fetchCount := int32(0)
	fetchFunc := func() (*sql.DB, string, error) {
		atomic.AddInt32(&fetchCount, 1)
		db, err := sql.Open("postgres", "host=localhost port=5432 user=postgres password=postgres dbname=postgres sslmode=disable")
		if err != nil {
			return nil, "", err
		}
		return db, "schema_tenant_a", nil
	}

	// First call: Cache miss -> calls fetchFunc
	db1, schema1, err := reg.GetOrFetch("tenant-a", fetchFunc)
	if err != nil {
		t.Fatalf("unexpected error on first fetch: %v", err)
	}
	if schema1 != "schema_tenant_a" || db1 == nil {
		t.Errorf("unexpected fetch result: db=%v, schema=%s", db1, schema1)
	}
	if atomic.LoadInt32(&fetchCount) != 1 {
		t.Errorf("expected fetchCount=1, got %d", fetchCount)
	}

	// Second call: Cache hit -> does NOT call fetchFunc again
	db2, schema2, err := reg.GetOrFetch("tenant-a", fetchFunc)
	if err != nil {
		t.Fatalf("unexpected error on second fetch: %v", err)
	}
	if db2 != db1 || schema2 != schema1 {
		t.Errorf("cache hit returned different handles: db1=%v db2=%v", db1, db2)
	}
	if atomic.LoadInt32(&fetchCount) != 1 {
		t.Errorf("expected fetchCount to remain 1 on cache hit, got %d", fetchCount)
	}

	// Clean up
	reg.Evict("tenant-a")
}

func TestPoolRegistry_GetOrFetch_FetchError(t *testing.T) {
	reg := registry.NewPoolRegistry()
	expectedErr := errors.New("database connection refused")

	fetchErrFunc := func() (*sql.DB, string, error) {
		return nil, "", expectedErr
	}

	db, schema, err := reg.GetOrFetch("tenant-err", fetchErrFunc)
	if err == nil {
		t.Fatalf("expected error from fetchDSN, got nil")
	}
	if db != nil || schema != "" {
		t.Errorf("expected nil db and empty schema on fetch error")
	}
}

func TestPoolRegistry_Singleflight_Coalescing(t *testing.T) {
	reg := registry.NewPoolRegistry()

	fetchCount := int32(0)
	fetchFunc := func() (*sql.DB, string, error) {
		atomic.AddInt32(&fetchCount, 1)
		time.Sleep(50 * time.Millisecond) // Simulate slow connection setup
		db, err := sql.Open("postgres", "host=localhost port=5432 user=postgres password=postgres dbname=postgres sslmode=disable")
		if err != nil {
			return nil, "", err
		}
		return db, "schema_concurrent", nil
	}

	numGoroutines := 50
	var wg sync.WaitGroup
	wg.Add(numGoroutines)

	for i := 0; i < numGoroutines; i++ {
		go func() {
			defer wg.Done()
			db, schema, err := reg.GetOrFetch("tenant-concurrent", fetchFunc)
			if err != nil {
				t.Errorf("unexpected error in goroutine: %v", err)
				return
			}
			if db == nil || schema != "schema_concurrent" {
				t.Errorf("invalid pool result in goroutine")
			}
		}()
	}

	wg.Wait()

	// Singleflight must coalesce all 50 calls into 1 execution of fetchFunc
	if atomic.LoadInt32(&fetchCount) != 1 {
		t.Errorf("expected singleflight to execute fetchFunc once, got %d times", fetchCount)
	}

	reg.Evict("tenant-concurrent")
}

func TestPoolRegistry_BoundedLRUEviction(t *testing.T) {
	reg := registry.NewPoolRegistry(registry.WithMaxCapacity(2))

	dummyFetch := func(id string) func() (*sql.DB, string, error) {
		return func() (*sql.DB, string, error) {
			db, err := sql.Open("postgres", "host=localhost port=5432 user=postgres password=postgres dbname=postgres sslmode=disable")
			if err != nil {
				return nil, "", err
			}
			return db, fmt.Sprintf("schema_%s", id), nil
		}
	}

	// 1. Add tenant-1 and tenant-2
	db1, schema1, err := reg.GetOrFetch("tenant-1", dummyFetch("tenant-1"))
	if err != nil {
		t.Fatalf("failed to fetch tenant-1: %v", err)
	}
	if schema1 != "schema_tenant-1" || db1 == nil {
		t.Errorf("unexpected tenant-1 result")
	}

	db2, _, err := reg.GetOrFetch("tenant-2", dummyFetch("tenant-2"))
	if err != nil {
		t.Fatalf("failed to fetch tenant-2: %v", err)
	}
	if db2 == nil {
		t.Errorf("unexpected tenant-2 result")
	}

	// Touch tenant-1 to update lastUsed, making tenant-2 the LRU entry
	time.Sleep(5 * time.Millisecond)
	_, _, _ = reg.GetOrFetch("tenant-1", dummyFetch("tenant-1"))

	// 2. Add tenant-3 -> maxCapacity (2) exceeded, tenant-2 should be evicted
	_, _, err = reg.GetOrFetch("tenant-3", dummyFetch("tenant-3"))
	if err != nil {
		t.Fatalf("failed to fetch tenant-3: %v", err)
	}

	// Evict remaining entries manually
	reg.Evict("tenant-1")
	reg.Evict("tenant-3")
}

func TestPoolRegistry_Evict(t *testing.T) {
	reg := registry.NewPoolRegistry()

	dummyFetch := func() (*sql.DB, string, error) {
		db, err := sql.Open("postgres", "host=localhost port=5432 user=postgres password=postgres dbname=postgres sslmode=disable")
		return db, "schema_evict", err
	}

	// Populate entry
	_, _, err := reg.GetOrFetch("tenant-evict", dummyFetch)
	if err != nil {
		t.Fatalf("failed to fetch pool: %v", err)
	}

	// Evict existing tenant
	reg.Evict("tenant-evict")

	// Evict non-existent tenant (should be safe no-op)
	reg.Evict("tenant-missing")
}

func TestPoolRegistry_SingleflightTimeout_HangingLeader(t *testing.T) {
	reg := registry.NewPoolRegistry(registry.WithFetchTimeout(50 * time.Millisecond))

	hangingFetch := func() (*sql.DB, string, error) {
		time.Sleep(1 * time.Second)
		return nil, "", fmt.Errorf("should have timed out")
	}

	done := make(chan error, 1)
	go func() {
		_, _, err := reg.GetOrFetch("tenant-hanging", hangingFetch)
		done <- err
	}()

	select {
	case err := <-done:
		if err == nil {
			t.Fatalf("expected error from hanging singleflight leader, got nil")
		}
		t.Logf("Successfully caught hanging singleflight leader timeout: %v", err)
	case <-time.After(200 * time.Millisecond):
		t.Fatalf("singleflight leader call blocked beyond fetchTimeout barrier!")
	}
}

func TestPoolRegistry_PurgeAll_And_Overflow(t *testing.T) {
	reg := registry.NewPoolRegistry()

	dummyFetch := func(id string) func() (*sql.DB, string, error) {
		return func() (*sql.DB, string, error) {
			db, err := sql.Open("postgres", "host=localhost port=5432 user=postgres password=postgres dbname=postgres sslmode=disable")
			return db, fmt.Sprintf("schema_%s", id), err
		}
	}

	// 1. Purge empty registry
	reg.PurgeAll()

	// 2. Populate entries and purge
	for i := 0; i < 5; i++ {
		tID := fmt.Sprintf("tenant-purge-%d", i)
		_, _, _ = reg.GetOrFetch(tID, dummyFetch(tID))
	}
	reg.PurgeAll()

	// 3. Test cleanupQueue overflow path:
	// cleanupQueue has capacity 5. We fill the channel by calling PurgeAll() multiple times rapidly,
	// then trigger the overflow path (select default branch spawning goroutine).
	for i := 0; i < 10; i++ {
		tID := fmt.Sprintf("tenant-overflow-%d", i)
		_, _, _ = reg.GetOrFetch(tID, dummyFetch(tID))
		reg.PurgeAll()
	}

	// Give background sweeper and overflow goroutines a brief window to complete drain
	time.Sleep(100 * time.Millisecond)
}

func TestPoolRegistry_Reaper_TTL(t *testing.T) {
	// Create registry with aggressive 50ms TTL
	reg := registry.NewPoolRegistry(registry.WithTTL(50 * time.Millisecond))

	dummyFetch := func() (*sql.DB, string, error) {
		db, err := sql.Open("postgres", "host=localhost port=5432 user=postgres password=postgres dbname=postgres sslmode=disable")
		return db, "schema_reap", err
	}

	// Add entry
	_, _, err := reg.GetOrFetch("tenant-reap", dummyFetch)
	if err != nil {
		t.Fatalf("failed to fetch pool: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	reg.StartReaper(ctx)

	// Wait for reaper sweep ticker and TTL to pass
	time.Sleep(200 * time.Millisecond)

	// Cancel context to test reaper termination & closeAll
	cancel()
	time.Sleep(50 * time.Millisecond)
}
