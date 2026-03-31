package service

import (
	"context"
	"database/sql"
	"fmt"
	"os"
)

// MigrationService loads a versioned SQL migration file and applies it
// idempotently against the service database on startup.
type MigrationService struct {
	migrationSQL string
}

// NewMigrationService reads the SQL migration file at migrationFilePath.
func NewMigrationService(migrationFilePath string) (*MigrationService, error) {
	migrationBytes, err := os.ReadFile(migrationFilePath)
	if err != nil {
		return nil, fmt.Errorf("failed to read migration file '%s': %w", migrationFilePath, err)
	}

	return &MigrationService{migrationSQL: string(migrationBytes)}, nil
}

// NewMigrationServiceFromSQL builds a MigrationService from an inline SQL string.
// Intended for tests; production code MUST load from a versioned migrations/ file.
func NewMigrationServiceFromSQL(migrationSQL string) *MigrationService {
	return &MigrationService{migrationSQL: migrationSQL}
}

// Migrate executes the loaded migration SQL idempotently against the database.
func (migrationService *MigrationService) Migrate(ctx context.Context, database *sql.DB) error {
	if _, err := database.ExecContext(ctx, migrationService.migrationSQL); err != nil {
		return fmt.Errorf("migration service: failed to execute schema migration: %w", err)
	}
	return nil
}
