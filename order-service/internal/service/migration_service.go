package service

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"os"
	"strings"

	_ "github.com/lib/pq"
)

type MigrationService struct {
	migrationSQL string
}

func NewMigrationService(migrationFilePath string) (*MigrationService, error) {
	migrationBytes, err := os.ReadFile(migrationFilePath)
	if err != nil {
		return nil, fmt.Errorf("failed to read migration file '%s': %w", migrationFilePath, err)
	}

	return NewMigrationServiceFromSQL(string(migrationBytes)), nil
}

func NewMigrationServiceFromSQL(migrationSQL string) *MigrationService {
	return &MigrationService{
		migrationSQL: migrationSQL,
	}
}

func (s *MigrationService) MigrateTenantDB(ctx context.Context, dsn, schemaName string) error {
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		return fmt.Errorf("failed to open database connection: %w", err)
	}
	defer db.Close()

	if err := db.PingContext(ctx); err != nil {
		return fmt.Errorf("failed to ping database: %w", err)
	}

	if schemaName != "" && schemaName != "public" {
		log.Printf("MigrationService: Ensuring schema '%s' exists...", schemaName)
		if _, err := db.ExecContext(ctx, fmt.Sprintf("CREATE SCHEMA IF NOT EXISTS %s;", schemaName)); err != nil {
			return fmt.Errorf("failed to create schema '%s': %w", schemaName, err)
		}
	}

	sqlStr := strings.ReplaceAll(s.migrationSQL, "{{SCHEMA_NAME}}", schemaName)
	isNonTransactional := strings.Contains(sqlStr, "-- tx: false") || strings.Contains(sqlStr, "-- migrate: no-transaction")

	if isNonTransactional {
		log.Printf("MigrationService: Executing non-transactional migration (e.g. CONCURRENTLY operations) for schema '%s'...", schemaName)
		if _, err := db.ExecContext(ctx, sqlStr); err != nil {
			return fmt.Errorf("failed to execute non-transactional SQL migration for schema '%s': %w", schemaName, err)
		}
	} else {
		log.Printf("MigrationService: Executing transactional migration for schema '%s'...", schemaName)
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			return fmt.Errorf("failed to begin DDL transaction for schema '%s': %w", schemaName, err)
		}

		if _, err := tx.ExecContext(ctx, sqlStr); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("failed to execute transactional SQL migration for schema '%s' (rolled back): %w", schemaName, err)
		}

		if err := tx.Commit(); err != nil {
			return fmt.Errorf("failed to commit DDL transaction for schema '%s': %w", schemaName, err)
		}
	}

	log.Printf("MigrationService: Successfully executed migrations for schema '%s'", schemaName)
	return nil
}
