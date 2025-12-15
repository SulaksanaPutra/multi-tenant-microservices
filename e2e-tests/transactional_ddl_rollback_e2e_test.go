package e2e_test

import (
	"context"
	"database/sql"
	"fmt"
	"testing"

	_ "github.com/lib/pq"
)

func TestE2E_TransactionalDDL_RollbackSafety(t *testing.T) {
	t.Log("=== E2E Test: Transactional DDL Migration Rollback Safety (TC-E2E-011 / Docs Case #4) ===")

	db, err := sql.Open("postgres", sharedDBDSN)
	if err != nil {
		t.Fatalf("Failed to connect to shared_db: %v", err)
	}
	defer db.Close()

	ctx := context.Background()
	testSchema := "tnt_rollback_test_schema"

	_, _ = db.ExecContext(ctx, fmt.Sprintf("CREATE SCHEMA IF NOT EXISTS %s;", testSchema))
	defer func() {
		_, _ = db.ExecContext(ctx, fmt.Sprintf("DROP SCHEMA IF EXISTS %s CASCADE;", testSchema))
	}()

	// 1. Begin DDL Transaction
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("Failed to begin DDL transaction: %v", err)
	}

	// 2. Create a valid table inside transaction
	_, err = tx.ExecContext(ctx, fmt.Sprintf("CREATE TABLE %s.valid_table_before_failure (id INT PRIMARY KEY);", testSchema))
	if err != nil {
		_ = tx.Rollback()
		t.Fatalf("Failed to execute initial valid DDL statement in tx: %v", err)
	}

	// 3. Execute invalid DDL statement inside same transaction (triggering syntax error midway)
	_, err = tx.ExecContext(ctx, fmt.Sprintf("CREATE TABLE %s.bad_table (id NON_EXISTENT_TYPE);", testSchema))
	if err == nil {
		_ = tx.Rollback()
		t.Fatalf("Expected SQL syntax error for invalid data type, got nil")
	}

	// 4. Rollback transaction cleanly
	_ = tx.Rollback()

	t.Logf("1. Triggered DDL syntax error midway through transaction and executed tx.Rollback()")

	// 5. Query PostgreSQL information_schema.tables to verify valid_table_before_failure was completely undone
	var tableCount int
	err = db.QueryRowContext(ctx, `
		SELECT COUNT(*) 
		FROM information_schema.tables 
		WHERE table_schema = $1 AND table_type = 'BASE TABLE';
	`, testSchema).Scan(&tableCount)
	if err != nil {
		t.Fatalf("Failed to query information_schema.tables: %v", err)
	}

	if tableCount > 0 {
		t.Fatalf("Transactional DDL Rollback failed! Expected 0 partial tables in schema '%s', got %d", testSchema, tableCount)
	}

	t.Logf("2. Verified Transactional DDL Rollback: Initial table creation was completely undone by ROLLBACK (table_count=%d)!", tableCount)
}
