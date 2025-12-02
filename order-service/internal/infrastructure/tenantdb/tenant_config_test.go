package tenantdb_test

import (
	"context"
	"database/sql"
	"testing"

	"order-service/internal/infrastructure/tenantdb"
)

func TestTenantConfig_Context(t *testing.T) {
	ctx := context.Background()

	// 1. Missing context test
	_, ok := tenantdb.FromContext(ctx)
	if ok {
		t.Fatalf("expected FromContext to return false for empty context")
	}

	// 2. Set and retrieve test
	dummyDB := &sql.DB{}
	expectedCfg := tenantdb.Config{
		TenantID:   "tenant-123",
		DB:         dummyDB,
		SchemaName: "tenant_123_schema",
	}

	ctxWithCfg := tenantdb.WithConfig(ctx, expectedCfg)
	retrievedCfg, ok := tenantdb.FromContext(ctxWithCfg)
	if !ok {
		t.Fatalf("expected FromContext to return true for context with config")
	}

	if retrievedCfg.TenantID != expectedCfg.TenantID {
		t.Errorf("expected TenantID '%s', got '%s'", expectedCfg.TenantID, retrievedCfg.TenantID)
	}
	if retrievedCfg.DB != expectedCfg.DB {
		t.Errorf("expected DB pointer %v, got %v", expectedCfg.DB, retrievedCfg.DB)
	}
	if retrievedCfg.SchemaName != expectedCfg.SchemaName {
		t.Errorf("expected SchemaName '%s', got '%s'", expectedCfg.SchemaName, retrievedCfg.SchemaName)
	}
}
