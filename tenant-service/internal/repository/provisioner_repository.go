package repository

import (
	"context"
	"fmt"
	"os"
	"strings"

	"tenant-service/internal/infrastructure/postgres"
	"tenant-service/internal/txctx"
)

type ProvisionerRepository interface {
	CreateSchema(ctx context.Context, schemaName string) error
	ExecuteMigration(ctx context.Context, schemaName, migrationFilePath string) error
	SeedOwnerMember(ctx context.Context, schemaName, userID, name, email string) error
}

type provisionerRepository struct {
	client *postgres.Client
}

func NewProvisionerRepository(client *postgres.Client) ProvisionerRepository {
	return &provisionerRepository{client: client}
}

func (r *provisionerRepository) CreateSchema(ctx context.Context, schemaName string) error {
	exec := txctx.GetExecutor(ctx, r.client.DB)
	query := fmt.Sprintf("CREATE SCHEMA IF NOT EXISTS %s;", schemaName)
	if _, err := exec.ExecContext(ctx, query); err != nil {
		return fmt.Errorf("failed to execute CREATE SCHEMA %s: %w", schemaName, err)
	}
	return nil
}

func (r *provisionerRepository) ExecuteMigration(ctx context.Context, schemaName, migrationFilePath string) error {
	exec := txctx.GetExecutor(ctx, r.client.DB)
	sqlBytes, err := os.ReadFile(migrationFilePath)
	if err != nil {
		return fmt.Errorf("failed to read migration file %s: %w", migrationFilePath, err)
	}

	migrationSQL := strings.ReplaceAll(string(sqlBytes), "{{SCHEMA_NAME}}", schemaName)
	if _, err := exec.ExecContext(ctx, migrationSQL); err != nil {
		return fmt.Errorf("failed to execute migration template for schema %s: %w", schemaName, err)
	}
	return nil
}

func (r *provisionerRepository) SeedOwnerMember(ctx context.Context, schemaName, userID, name, email string) error {
	exec := txctx.GetExecutor(ctx, r.client.DB)
	query := fmt.Sprintf(`
		INSERT INTO %s.tenant_members (user_id, name, email, role)
		VALUES ($1, $2, $3, 'owner')
		ON CONFLICT (user_id) DO NOTHING;
	`, schemaName)
	if _, err := exec.ExecContext(ctx, query, userID, name, email); err != nil {
		return fmt.Errorf("failed to seed owner member into %s.tenant_members: %w", schemaName, err)
	}
	return nil
}
