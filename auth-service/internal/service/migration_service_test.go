package service

import (
	"strings"
	"testing"
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

func TestMigrationService_NewMigrationServiceFromSQL(t *testing.T) {
	ms := NewMigrationServiceFromSQL("CREATE TABLE IF NOT EXISTS public.dummy (id INT);")
	if ms == nil {
		t.Fatalf("expected non-nil MigrationService")
	}
	if !strings.Contains(ms.migrationSQL, "CREATE TABLE IF NOT EXISTS public.dummy") {
		t.Errorf("unexpected migration SQL: %s", ms.migrationSQL)
	}
}
