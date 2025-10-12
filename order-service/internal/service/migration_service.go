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

type MigrationService interface {
	MigrateTenantDB(ctx context.Context, dsn, schemaName string) error
}

type migrationService struct {
	migrationSQL string
}

func NewMigrationService(migrationFilePath string) (MigrationService, error) {
	migrationBytes, err := os.ReadFile(migrationFilePath)
	if err != nil {
		return nil, fmt.Errorf("failed to read migration file '%s': %w", migrationFilePath, err)
	}

	return &migrationService{
		migrationSQL: string(migrationBytes),
	}, nil
}

func (s *migrationService) MigrateTenantDB(ctx context.Context, dsn, schemaName string) error {
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
	if _, err := db.ExecContext(ctx, sqlStr); err != nil {
		return fmt.Errorf("failed to execute SQL migration for schema '%s': %w", schemaName, err)
	}

	log.Printf("MigrationService: Successfully executed migrations for schema '%s'", schemaName)
	return nil
}
