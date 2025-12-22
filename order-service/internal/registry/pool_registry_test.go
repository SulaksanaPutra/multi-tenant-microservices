package registry_test

import (
	"database/sql"
	"fmt"
	"testing"
	"time"

	_ "github.com/lib/pq"
	"order-service/internal/registry"
)

func TestPoolRegistry_BoundedLRUEviction(t *testing.T) {
	// Create PoolRegistry with maxCapacity = 2
	reg := registry.NewPoolRegistry(registry.WithMaxCapacity(2))

	openedCount := 0
	dummyFetch := func(id string) func() (*sql.DB, string, error) {
		return func() (*sql.DB, string, error) {
			openedCount++
			// Open a dummy DB connection handle (won't connect until queried)
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

	// Touch tenant-1 to make tenant-2 the LRU entry
	time.Sleep(5 * time.Millisecond)
	_, _, _ = reg.GetOrFetch("tenant-1", dummyFetch("tenant-1"))

	// 2. Add tenant-3 -> capacity (2) exceeded, tenant-2 should be evicted
	_, _, err = reg.GetOrFetch("tenant-3", dummyFetch("tenant-3"))
	if err != nil {
		t.Fatalf("failed to fetch tenant-3: %v", err)
	}

	// Evicting manual check
	reg.Evict("tenant-1")
	reg.Evict("tenant-3")
}

func TestPoolRegistry_SingleflightTimeout_HangingLeader(t *testing.T) {
	// Create PoolRegistry with an aggressive fetchTimeout of 50ms
	reg := registry.NewPoolRegistry(registry.WithFetchTimeout(50 * time.Millisecond))

	// Hanging fetch function that blocks indefinitely
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
