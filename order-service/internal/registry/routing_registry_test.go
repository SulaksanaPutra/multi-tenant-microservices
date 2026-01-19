package registry_test

import (
	"fmt"
	"sync"
	"testing"

	"order-service/internal/registry"
)

func TestRoutingRegistry_Set_Validation(t *testing.T) {
	reg := registry.NewRoutingRegistry()

	// Empty TenantID should be ignored
	emptyMeta := registry.RoutingMetadata{
		TenantID:   "",
		DBHost:     "localhost",
		DBPort:     5432,
		DBName:     "tenant_db",
		DBUser:     "postgres",
		SchemaName: "tenant_schema",
	}
	reg.Set(emptyMeta)

	_, ok := reg.Get("")
	if ok {
		t.Errorf("expected empty TenantID metadata to not be stored")
	}

	// Valid TenantID should be stored
	validMeta := registry.RoutingMetadata{
		TenantID:   "tenant-valid",
		DBHost:     "10.0.0.1",
		DBPort:     5432,
		DBName:     "db_valid",
		DBUser:     "user_valid",
		SchemaName: "schema_valid",
	}
	reg.Set(validMeta)

	got, ok := reg.Get("tenant-valid")
	if !ok {
		t.Fatalf("expected tenant-valid to exist")
	}
	if got != validMeta {
		t.Errorf("got metadata %+v, expected %+v", got, validMeta)
	}
}

func TestRoutingRegistry_Get_NotFound(t *testing.T) {
	reg := registry.NewRoutingRegistry()

	meta, ok := reg.Get("non-existent-tenant")
	if ok {
		t.Errorf("expected ok=false for non-existent tenant, got ok=true")
	}
	if meta != (registry.RoutingMetadata{}) {
		t.Errorf("expected zero-value RoutingMetadata, got %+v", meta)
	}
}

func TestRoutingRegistry_Delete(t *testing.T) {
	reg := registry.NewRoutingRegistry()

	meta := registry.RoutingMetadata{
		TenantID:   "tenant-del",
		DBHost:     "localhost",
		DBPort:     5432,
		DBName:     "tenant_db",
		DBUser:     "postgres",
		SchemaName: "tenant_schema",
	}
	reg.Set(meta)

	// Delete existing
	reg.Delete("tenant-del")
	_, ok := reg.Get("tenant-del")
	if ok {
		t.Errorf("expected tenant-del to be deleted")
	}

	// Delete non-existent (safe no-op)
	reg.Delete("tenant-missing")
}

func TestRoutingRegistry_PurgeAll(t *testing.T) {
	reg := registry.NewRoutingRegistry()

	// 1. Purge empty registry
	reg.PurgeAll()

	// 2. Populate and purge
	for i := 0; i < 10; i++ {
		tID := fmt.Sprintf("tenant-%d", i)
		reg.Set(registry.RoutingMetadata{
			TenantID:   tID,
			DBHost:     "localhost",
			DBPort:     5432,
			DBName:     "db",
			DBUser:     "user",
			SchemaName: "schema",
		})
	}

	_, ok := reg.Get("tenant-5")
	if !ok {
		t.Fatalf("expected tenant-5 to exist before purge")
	}

	reg.PurgeAll()

	for i := 0; i < 10; i++ {
		tID := fmt.Sprintf("tenant-%d", i)
		if _, ok := reg.Get(tID); ok {
			t.Errorf("expected %s to be purged", tID)
		}
	}
}

func TestRoutingRegistry_ConcurrentOps(t *testing.T) {
	reg := registry.NewRoutingRegistry()

	var wg sync.WaitGroup
	numGoroutines := 100

	// Concurrent writes
	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			tID := fmt.Sprintf("tenant-%d", id%10)
			meta := registry.RoutingMetadata{
				TenantID:   tID,
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
		go func(id int) {
			defer wg.Done()
			tID := fmt.Sprintf("tenant-%d", id%10)
			_, _ = reg.Get(tID)
		}(i)
	}

	// Concurrent deletes
	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			tID := fmt.Sprintf("tenant-%d", id%10)
			reg.Delete(tID)
		}(i)
	}

	// Concurrent purges
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			reg.PurgeAll()
		}()
	}

	wg.Wait()
}
