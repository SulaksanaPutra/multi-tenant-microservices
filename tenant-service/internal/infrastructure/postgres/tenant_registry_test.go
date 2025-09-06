package postgres

import (
	"sync"
	"testing"
)

func TestConnectionRegistry_SharedTenantReturnsDefaultPool(t *testing.T) {
	registry := NewConnectionRegistry(nil)

	tenantMeta := &TenantMetadata{
		ID:            "tenant_shared",
		PlacementType: "SHARED",
	}

	db, err := registry.GetConnection(tenantMeta)
	if err != nil {
		t.Fatalf("unexpected error for shared tenant: %v", err)
	}
	if db != nil {
		t.Fatalf("expected nil DB for mock nil shared DB pool")
	}
}

func TestConnectionRegistry_ThreadSafety(t *testing.T) {
	registry := NewConnectionRegistry(nil)
	var wg sync.WaitGroup

	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			meta := &TenantMetadata{
				ID:            "tenant_shared",
				PlacementType: "SHARED",
			}
			_, _ = registry.GetConnection(meta)
		}()
	}
	wg.Wait()
}
