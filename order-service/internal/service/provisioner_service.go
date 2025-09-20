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

const (
	sharedSchemaPrefix = "order_db_"
)

type ProvisionerService interface {
	ProvisionShared(ctx context.Context, tenantID string) (dsn string, schemaName string, err error)
	ProvisionDedicated(ctx context.Context, tenantID string) (dsn string, err error)
}

type DockerProvisioner interface {
	CheckOrCreate(ctx context.Context, tenantID string) (hostPort string, err error)
}

type provisionerService struct {
	sharedProvisionerDSN string // Admin DSN template for sharedDB host (opened on demand during provisioning)
	dockerClient         DockerProvisioner
	migrationSQL         string // content of the orders migration file
}

type ProvisionerServiceParams struct {
	SharedProvisionerDSN string
	DockerClient         DockerProvisioner
	MigrationFile        string // path to migrations/001_create_orders.sql
}

func NewProvisionerService(params ProvisionerServiceParams) (ProvisionerService, error) {
	migrationBytes, err := os.ReadFile(params.MigrationFile)
	if err != nil {
		return nil, fmt.Errorf("failed to read migration file '%s': %w", params.MigrationFile, err)
	}
	return &provisionerService{
		sharedProvisionerDSN: params.SharedProvisionerDSN,
		dockerClient:         params.DockerClient,
		migrationSQL:         string(migrationBytes),
	}, nil
}

func (s *provisionerService) ProvisionShared(ctx context.Context, tenantID string) (string, string, error) {
	schemaName := sharedSchemaPrefix + sanitizeTenantID(tenantID)

	log.Printf("ProvisionerService: Connecting dynamically to sharedDB host for tenant '%s'...", tenantID)

	// Open on-demand temporary connection to sharedDB host
	db, err := sql.Open("postgres", s.sharedProvisionerDSN)
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
	return s.sharedProvisionerDSN, schemaName, nil
}

func (s *provisionerService) ProvisionDedicated(ctx context.Context, tenantID string) (string, error) {
	hostPort, err := s.dockerClient.CheckOrCreate(ctx, tenantID)
	if err != nil {
		return "", fmt.Errorf("docker provisioning failed for tenant '%s': %w", tenantID, err)
	}

	dsn := fmt.Sprintf("host=localhost port=%s user=postgres password=postgres dbname=orders sslmode=disable", hostPort)

	// Open on-demand temporary connection to dedicated DB container
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		return "", fmt.Errorf("failed to open dedicated DB for tenant '%s': %w", tenantID, err)
	}
	defer db.Close()

	if err := s.runMigrations(ctx, db, "public"); err != nil {
		return "", fmt.Errorf("migration failed on dedicated DB for tenant '%s': %w", tenantID, err)
	}

	log.Printf("ProvisionerService: Dedicated DB provisioned for tenant '%s' on port %s", tenantID, hostPort)
	return dsn, nil
}

func (s *provisionerService) runMigrations(ctx context.Context, db *sql.DB, schemaName string) error {
	sqlStr := strings.ReplaceAll(s.migrationSQL, "{{SCHEMA_NAME}}", schemaName)
	if _, err := db.ExecContext(ctx, sqlStr); err != nil {
		return fmt.Errorf("failed to execute migration: %w", err)
	}
	return nil
}

func sanitizeTenantID(id string) string {
	return strings.ReplaceAll(strings.ToLower(id), "-", "_")
}
