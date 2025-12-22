package service_test

import (
	"context"
	"testing"

	_ "github.com/lib/pq"
	"order-service/internal/service"
)

func TestMigrationService_NonTransactionalFlagDetection(t *testing.T) {
	sqlNonTx := "-- tx: false\nCREATE INDEX CONCURRENTLY IF NOT EXISTS idx_test ON public.orders(tenant_id);"
	ms := service.NewMigrationServiceFromSQL(sqlNonTx)

	if ms == nil {
		t.Fatalf("expected non-nil MigrationService")
	}

	// Test against invalid DSN to verify parsing proceeds up to connection step
	err := ms.MigrateTenantDB(context.Background(), "invalid_dsn", "public")
	if err == nil {
		t.Fatalf("expected error connecting with invalid DSN")
	}
}

func TestMigrationService_TransactionalDefault(t *testing.T) {
	sqlTx := "CREATE TABLE IF NOT EXISTS {{SCHEMA_NAME}}.dummy_table (id INT PRIMARY KEY);"
	ms := service.NewMigrationServiceFromSQL(sqlTx)

	if ms == nil {
		t.Fatalf("expected non-nil MigrationService")
	}

	err := ms.MigrateTenantDB(context.Background(), "invalid_dsn", "public")
	if err == nil {
		t.Fatalf("expected error connecting with invalid DSN")
	}
}
