/*
 * Test Specification: TC-E2E-011 - Transactional DDL Migration Rollback Safety
 * Architectural Scope: PostgreSQL DDL engine, Database Migration Runner
 * Objective: Verify database schema safety and atomic rollback guarantees when SQL DDL migrations fail midway,
 *            ensuring PostgreSQL transactions roll back all partial table creations atomically.
 * Failure Mode Guarded: Partially applied database schema migrations, orphaned schema objects, corrupted table structures.
 *
 * Workflow / How It Works:
 *   1. Create an isolated PostgreSQL schema (tnt_rollback_test_schema) on shared_db connection.
 *   2. Begin explicit DDL transaction (db.BeginTx).
 *   3. Execute valid table creation statement (CREATE TABLE ... valid_table_before_failure).
 *   4. Execute invalid DDL statement (CREATE TABLE ... with invalid SQL data type), causing an intentional SQL syntax error.
 *   5. Invoke tx.Rollback() to abort the transaction.
 *   6. Query information_schema.tables to verify valid_table_before_failure was completely undone (table_count=0).
 */

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

	// =========================================================================
	// Step 1: Connect to Database & Create Isolated Test Schema
	// Instruction: Connect to shared_db and execute CREATE SCHEMA IF NOT EXISTS.
	// =========================================================================
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

	// =========================================================================
	// Step 2: Begin DDL Transaction & Execute Initial Valid DDL Statement
	// Instruction: Call db.BeginTx and create valid table valid_table_before_failure.
	// Architectural Invariant: PostgreSQL supports fully transactional DDL (unlike MySQL).
	// =========================================================================
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("Failed to begin DDL transaction: %v", err)
	}

	_, err = tx.ExecContext(ctx, fmt.Sprintf("CREATE TABLE %s.valid_table_before_failure (id INT PRIMARY KEY);", testSchema))
	if err != nil {
		_ = tx.Rollback()
		t.Fatalf("Failed to execute initial valid DDL statement in tx: %v", err)
	}

	// =========================================================================
	// Step 3: Trigger DDL Syntax Error & Execute Rollback
	// Instruction: Execute DDL statement with invalid data type NON_EXISTENT_TYPE inside the same transaction.
	// Architectural Invariant: Mid-transaction error aborts transaction state, enabling tx.Rollback().
	// =========================================================================
	_, err = tx.ExecContext(ctx, fmt.Sprintf("CREATE TABLE %s.bad_table (id NON_EXISTENT_TYPE);", testSchema))
	if err == nil {
		_ = tx.Rollback()
		t.Fatalf("Expected SQL syntax error for invalid data type, got nil")
	}

	_ = tx.Rollback()
	t.Logf("1. Triggered DDL syntax error midway through transaction and executed tx.Rollback()")

	// =========================================================================
	// Step 4: Query Information Schema to Verify Zero Partial Tables
	// Instruction: Query information_schema.tables for testSchema and assert count == 0.
	// Architectural Invariant: Transactional rollback completely undoes valid_table_before_failure creation.
	// =========================================================================
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
