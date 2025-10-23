package registry_test

import (
	"sync"
	"testing"

	"order-service/internal/registry"
)

func TestRoutingRegistry_ConcurrentSetGet(t *testing.T) {
	reg := registry.NewRoutingRegistry()

	var wg sync.WaitGroup
	numGoroutines := 100

	// Concurrent writes
	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			meta := registry.RoutingMetadata{
				TenantID:   "tenant-1",
				DBHost:     "localhost",
				DBPort:     5432,
				DBName:     "tenant_db",
				DBUser:     "postgres",
				SchemaName: "tenant_schema",
			}
			reg.Set(meta)
		}(i)
	}

	// Concurrent reads
	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = reg.Get("tenant-1")
		}()
	}

	wg.Wait()

	meta, ok := reg.Get("tenant-1")
	if !ok {
		t.Fatalf("expected tenant-1 to exist in RoutingRegistry")
	}
	if meta.DBHost != "localhost" || meta.DBPort != 5432 {
		t.Errorf("unexpected metadata: %+v", meta)
	}

	// Test Delete
	reg.Delete("tenant-1")
	_, ok = reg.Get("tenant-1")
	if ok {
		t.Errorf("expected tenant-1 to be deleted from RoutingRegistry")
	}
}
