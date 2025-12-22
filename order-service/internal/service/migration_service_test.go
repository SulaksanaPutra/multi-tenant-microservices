package service

import (
	"context"
	"strings"
	"testing"

	_ "github.com/lib/pq"
)

func TestMigrationService_NewMigrationService_FileNotFound(t *testing.T) {
	ms, err := NewMigrationService("non_existent_file.sql")
	if err == nil {
		t.Fatalf("expected error reading non-existent file, got nil")
	}
	if ms != nil {
		t.Fatalf("expected nil MigrationService on error")
	}
}

func TestMigrationService_EmptySchemaNameNormalization(t *testing.T) {
	rawSQL := "CREATE TABLE {{SCHEMA_NAME}}.orders (id INT);"
	ms := NewMigrationServiceFromSQL(rawSQL)

	t.Run("empty schema defaults to public", func(t *testing.T) {
		result := ms.BuildMigrationSQL("")
		if !strings.Contains(result, "public.orders") {
			t.Errorf("expected 'public.orders' in substituted SQL, got '%s'", result)
		}
	})

	t.Run("whitespace schema defaults to public", func(t *testing.T) {
		result := ms.BuildMigrationSQL("   ")
		if !strings.Contains(result, "public.orders") {
			t.Errorf("expected 'public.orders' in substituted SQL, got '%s'", result)
		}
	})

	t.Run("explicit custom schema replaces placeholder", func(t *testing.T) {
		result := ms.BuildMigrationSQL("tenant_acme")
		if !strings.Contains(result, "tenant_acme.orders") {
			t.Errorf("expected 'tenant_acme.orders' in substituted SQL, got '%s'", result)
		}
	})
}

func TestMigrationService_NonTransactionalFlagDetection(t *testing.T) {
	sqlNonTx := "-- tx: false\nCREATE INDEX CONCURRENTLY IF NOT EXISTS idx_test ON public.orders(tenant_id);"
	ms := NewMigrationServiceFromSQL(sqlNonTx)

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
	ms := NewMigrationServiceFromSQL(sqlTx)

	if ms == nil {
		t.Fatalf("expected non-nil MigrationService")
	}

	err := ms.MigrateTenantDB(context.Background(), "invalid_dsn", "public")
	if err == nil {
		t.Fatalf("expected error connecting with invalid DSN")
	}
}
