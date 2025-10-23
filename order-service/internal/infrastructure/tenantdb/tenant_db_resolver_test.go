package tenantdb_test

import (
	"context"
	"net/http"
	"net/http/httptest"
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
	resolver := tenantdb.NewResolver(tenantdb.ResolverParams{
		PoolRegistry:     poolRegistry,
		RoutingRegistry:  routingRegistry,
		TenantServiceURL: ts.URL,
	})

	ctx := context.Background()
	_, _, err := resolver.GetTenantDB(ctx, "tenant-err")
	if err == nil {
		t.Fatal("expected error when tenant-service fails, got nil")
	}
}
