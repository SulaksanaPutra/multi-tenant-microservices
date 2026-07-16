package tenantdb_test

import (
	"context"
	"database/sql"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"order-service/internal/infrastructure/tenantdb"
	"order-service/internal/registry"
)

func TestTenantDBResolver_TenantServiceError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer ts.Close()

	poolRegistry := registry.NewPoolRegistry()
	routingRegistry := registry.NewRoutingRegistry()
	resolver := tenantdb.NewResolver(tenantdb.Params{
		PoolRegistry:     poolRegistry,
		RoutingRegistry:  routingRegistry,
		TenantServiceURL: ts.URL,
	})

	ctx := context.Background()
	_, err := resolver.GetTenantDB(ctx, "tenant-err")
	if err == nil {
		t.Fatal("expected error when tenant-service fails, got nil")
	}
}

func TestTenantDBResolver_FastPath_CachedDedicated(t *testing.T) {
	poolRegistry := registry.NewPoolRegistry()
	routingRegistry := registry.NewRoutingRegistry()

	// Pre-seed cached metadata in RoutingRegistry with dedicated host
	routingRegistry.Set(registry.RoutingMetadata{
		TenantID:   "tenant-cached",
		DBHost:     "dedicated-node-1",
		DBPort:     5432,
		DBName:     "testdb",
		DBUser:     "postgres",
		SchemaName: "tenant_cached_schema",
	})

	dummyDB := &sql.DB{}
	// Pre-seed dummy pool in PoolRegistry
	_, _, _ = poolRegistry.GetOrFetch("tenant-cached", func() (*sql.DB, string, error) {
		return dummyDB, "tenant_cached_schema", nil
	})

	resolver := tenantdb.NewResolver(tenantdb.Params{
		PoolRegistry:    poolRegistry,
		RoutingRegistry: routingRegistry,
	})

	ctx := context.Background()
	cfg, err := resolver.GetTenantDB(ctx, "tenant-cached")
	if err != nil {
		t.Fatalf("unexpected error resolving cached tenant: %v", err)
	}

	if cfg.TenantID != "tenant-cached" {
		t.Errorf("expected TenantID 'tenant-cached', got '%s'", cfg.TenantID)
	}
	if cfg.SchemaName != "tenant_cached_schema" {
		t.Errorf("expected SchemaName 'tenant_cached_schema', got '%s'", cfg.SchemaName)
	}
	if cfg.DB != dummyDB {
		t.Errorf("expected DB handle %v, got %v", dummyDB, cfg.DB)
	}
}

func TestTenantDBResolver_FetchRouting_Success(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token := r.Header.Get("X-Internal-Service-Token")
		if token != "test_token" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{
			"data": {
				"db_host": "dedicated-node-2",
				"db_port": 5432,
				"db_name": "postgres",
				"db_user": "postgres",
				"schema_name": "tenant_123_schema"
			}
		}`))
	}))
	defer ts.Close()

	poolRegistry := registry.NewPoolRegistry()
	routingRegistry := registry.NewRoutingRegistry()

	dummyDB := &sql.DB{}
	// Pre-seed pool for tenant-123 so resolution succeeds after fetching routing metadata
	_, _, _ = poolRegistry.GetOrFetch("tenant-123", func() (*sql.DB, string, error) {
		return dummyDB, "tenant_123_schema", nil
	})

	resolver := tenantdb.NewResolver(tenantdb.Params{
		PoolRegistry:         poolRegistry,
		RoutingRegistry:      routingRegistry,
		TenantServiceURL:     ts.URL,
		InternalServiceToken: "test_token",
	})

	ctx := context.Background()
	cfg, err := resolver.GetTenantDB(ctx, "tenant-123")
	if err != nil {
		t.Fatalf("unexpected error resolving DB: %v", err)
	}

	if cfg.SchemaName != "tenant_123_schema" {
		t.Errorf("expected SchemaName 'tenant_123_schema', got '%s'", cfg.SchemaName)
	}

	// Verify metadata was cached in RoutingRegistry
	meta, ok := routingRegistry.Get("tenant-123")
	if !ok {
		t.Fatalf("expected RoutingRegistry to contain cached metadata after fetch")
	}
	if meta.DBHost != "dedicated-node-2" {
		t.Errorf("expected DBHost 'dedicated-node-2', got '%s'", meta.DBHost)
	}
}

func TestTenantDBResolver_SharedPlanRoutesToSharedPoolEvenOffSharedHost(t *testing.T) {
	poolRegistry := registry.NewPoolRegistry()
	routingRegistry := registry.NewRoutingRegistry()
	// Premium tier: shared_db lives on data-plane-db (not a "shared host").
	routingRegistry.Set(registry.RoutingMetadata{
		TenantID:   "tenant-prem-shared",
		DBHost:     "data-plane-db",
		DBPort:     5432,
		DBName:     "shared_db",
		DBUser:     "postgres",
		SchemaName: "tenant_prem_shared_order_db",
	})

	dummyDB := &sql.DB{}
	_, _, _ = poolRegistry.GetOrFetch("tenant-prem-shared", func() (*sql.DB, string, error) {
		return dummyDB, "tenant_prem_shared_order_db", nil
	})

	resolver := tenantdb.NewResolver(tenantdb.Params{
		PoolRegistry:    poolRegistry,
		RoutingRegistry: routingRegistry,
	})

	ctx := context.Background()
	_, err := resolver.GetTenantDB(ctx, "tenant-prem-shared")
	if err == nil {
		t.Fatal("shared-plan tenant must be routed to the shared pool (which requires a real DB), not the pre-seeded per-tenant pool")
	}
	if !strings.Contains(err.Error(), "shared pool") {
		t.Fatalf("expected error from the shared pool path, got: %v", err)
	}
}

func TestTenantDBResolver_SameInstanceDedicatedUsesPerTenantPool(t *testing.T) {
	poolRegistry := registry.NewPoolRegistry()
	routingRegistry := registry.NewRoutingRegistry()
	routingRegistry.Set(registry.RoutingMetadata{
		TenantID:   "tenant-same",
		DBHost:     "postgres",
		DBPort:     5432,
		DBName:     "tenant_same_order_db",
		DBUser:     "tenant_same_order_user",
		SchemaName: "public",
	})

	dummyDB := &sql.DB{}
	_, _, _ = poolRegistry.GetOrFetch("tenant-same", func() (*sql.DB, string, error) {
		return dummyDB, "public", nil
	})

	resolver := tenantdb.NewResolver(tenantdb.Params{
		PoolRegistry:    poolRegistry,
		RoutingRegistry: routingRegistry,
	})

	ctx := context.Background()
	cfg, err := resolver.GetTenantDB(ctx, "tenant-same")
	if err != nil {
		t.Fatalf("unexpected error resolving same-instance dedicated tenant: %v", err)
	}
	if cfg.DB != dummyDB {
		t.Errorf("same-instance dedicated tenant must use its per-tenant pool, got %v", cfg.DB)
	}
	if cfg.SchemaName != "public" {
		t.Errorf("expected SchemaName 'public', got '%s'", cfg.SchemaName)
	}
}

func TestTenantDBResolver_InvalidJSON(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`invalid-json`))
	}))
	defer ts.Close()

	poolRegistry := registry.NewPoolRegistry()
	routingRegistry := registry.NewRoutingRegistry()
	resolver := tenantdb.NewResolver(tenantdb.Params{
		PoolRegistry:     poolRegistry,
		RoutingRegistry:  routingRegistry,
		TenantServiceURL: ts.URL,
	})

	ctx := context.Background()
	_, err := resolver.GetTenantDB(ctx, "tenant-invalid")
	if err == nil {
		t.Fatal("expected error parsing invalid JSON, got nil")
	}
}

func TestTenantDBResolver_EmptyDBHost(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"data": {"db_host": ""}}`))
	}))
	defer ts.Close()

	poolRegistry := registry.NewPoolRegistry()
	routingRegistry := registry.NewRoutingRegistry()
	resolver := tenantdb.NewResolver(tenantdb.Params{
		PoolRegistry:     poolRegistry,
		RoutingRegistry:  routingRegistry,
		TenantServiceURL: ts.URL,
	})

	ctx := context.Background()
	_, err := resolver.GetTenantDB(ctx, "tenant-empty")
	if err == nil {
		t.Fatal("expected error for empty db_host, got nil")
	}
}
