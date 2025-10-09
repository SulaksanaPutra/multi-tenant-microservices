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

type ProvisionerService interface {
	ProvisionShared(ctx context.Context, tenantID string) (dsn string, schemaName string, err error)
	ProvisionDedicated(ctx context.Context, tenantID string) (dsn string, err error)
}

type provisionerService struct {
	adminDSN     string // Admin DSN template for primary PostgreSQL host
	migrationSQL string // content of the orders migration file
}

type ProvisionerServiceParams struct {
	SharedProvisionerDSN string
	MigrationFile        string // path to migrations/001_create_orders.sql
}

func NewProvisionerService(params ProvisionerServiceParams) (ProvisionerService, error) {
	migrationBytes, err := os.ReadFile(params.MigrationFile)
	if err != nil {
		return nil, fmt.Errorf("failed to read migration file '%s': %w", params.MigrationFile, err)
	}
	return &provisionerService{
		adminDSN:     params.SharedProvisionerDSN,
		migrationSQL: string(migrationBytes),
	}, nil
}

func (s *provisionerService) ProvisionShared(ctx context.Context, tenantID string) (string, string, error) {
	schemaName := fmt.Sprintf("%s_order_db", sanitizeTenantID(tenantID))

	log.Printf("ProvisionerService: Connecting dynamically to sharedDB host for tenant '%s'...", tenantID)

	// Open on-demand temporary connection to sharedDB host
	db, err := sql.Open("postgres", s.adminDSN)
	if err != nil {
		return "", "", fmt.Errorf("failed to connect to sharedDB host on demand: %w", err)
	}
	defer db.Close()

	if err := db.PingContext(ctx); err != nil {
		return "", "", fmt.Errorf("failed to ping sharedDB host: %w", err)
	}

	log.Printf("ProvisionerService: Creating shared schema '%s' for tenant '%s'", schemaName, tenantID)

	// CREATE SCHEMA IF NOT EXISTS is idempotent — safe to retry.
	if _, err := db.ExecContext(ctx, fmt.Sprintf("CREATE SCHEMA IF NOT EXISTS %s;", schemaName)); err != nil {
		return "", "", fmt.Errorf("failed to create schema '%s': %w", schemaName, err)
	}

	// Run migrations with the schema substituted in.
	if err := s.runMigrations(ctx, db, schemaName); err != nil {
		return "", "", fmt.Errorf("migration failed for schema '%s': %w", schemaName, err)
	}

	log.Printf("ProvisionerService: Shared schema '%s' provisioned for tenant '%s'", schemaName, tenantID)
	return s.adminDSN, schemaName, nil
}

func (s *provisionerService) ProvisionDedicated(ctx context.Context, tenantID string) (string, error) {
	dbName := fmt.Sprintf("%s_order_db", sanitizeTenantID(tenantID))

	log.Printf("ProvisionerService: Connecting to primary postgres host to provision dedicated DB '%s'...", dbName)

	// 1. Connect to primary Postgres instance via admin DSN
	adminDB, err := sql.Open("postgres", s.adminDSN)
	if err != nil {
		return "", fmt.Errorf("failed to connect to admin DB: %w", err)
	}
	defer adminDB.Close()

	if err := adminDB.PingContext(ctx); err != nil {
		return "", fmt.Errorf("failed to ping admin DB host: %w", err)
	}

	// 2. Check if dedicated database already exists
	var exists bool
	checkQuery := "SELECT EXISTS(SELECT 1 FROM pg_database WHERE datname = $1);"
	if err := adminDB.QueryRowContext(ctx, checkQuery, dbName).Scan(&exists); err != nil {
		return "", fmt.Errorf("failed to check existing database '%s': %w", dbName, err)
	}

	if !exists {
		log.Printf("ProvisionerService: Creating dedicated database '%s' for tenant '%s'...", dbName, tenantID)
		if _, err := adminDB.ExecContext(ctx, fmt.Sprintf("CREATE DATABASE %s;", dbName)); err != nil {
			return "", fmt.Errorf("failed to create dedicated database '%s': %w", dbName, err)
		}
	} else {
		log.Printf("ProvisionerService: Dedicated database '%s' already exists.", dbName)
	}

	// 3. Construct dedicated DSN on the same postgres host
	dedicatedDSN := replaceDBName(s.adminDSN, dbName)

	// 4. Open connection to newly created dedicated database and run migrations
	dedicatedDB, err := sql.Open("postgres", dedicatedDSN)
	if err != nil {
		return "", fmt.Errorf("failed to open dedicated DB '%s': %w", dbName, err)
	}
	defer dedicatedDB.Close()

	if err := s.runMigrations(ctx, dedicatedDB, "public"); err != nil {
		return "", fmt.Errorf("migration failed on dedicated DB '%s': %w", dbName, err)
	}

	log.Printf("ProvisionerService: Dedicated DB '%s' provisioned successfully for tenant '%s'", dbName, tenantID)
	return dedicatedDSN, nil
}

func (s *provisionerService) runMigrations(ctx context.Context, db *sql.DB, schemaName string) error {
	sqlStr := strings.ReplaceAll(s.migrationSQL, "{{SCHEMA_NAME}}", schemaName)
	if _, err := db.ExecContext(ctx, sqlStr); err != nil {
		return fmt.Errorf("failed to execute migration: %w", err)
	}
	return nil
}

func replaceDBName(dsn, newDBName string) string {
	parts := strings.Split(dsn, " ")
	found := false
	for i, p := range parts {
		if strings.HasPrefix(p, "dbname=") {
			parts[i] = "dbname=" + newDBName
			found = true
			break
		}
	}
	if !found {
		parts = append(parts, "dbname="+newDBName)
	}
	return strings.Join(parts, " ")
}

func sanitizeTenantID(id string) string {
	return strings.ReplaceAll(strings.ToLower(id), "-", "_")
}
