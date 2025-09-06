package repository

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"strings"

	"tenant-service/internal/infrastructure/postgres"
)

type ProvisionerRepository interface {
	CreateSchemaTx(ctx context.Context, tx *sql.Tx, schemaName string) error
	ExecuteMigrationTx(ctx context.Context, tx *sql.Tx, schemaName, migrationFilePath string) error
	SeedOwnerMemberTx(ctx context.Context, tx *sql.Tx, schemaName, userID, name, email string) error
}

type postgresProvisionerRepository struct {
	client *postgres.Client
}

func NewProvisionerRepository(client *postgres.Client) ProvisionerRepository {
	return &postgresProvisionerRepository{client: client}
}

func (r *postgresProvisionerRepository) CreateSchemaTx(ctx context.Context, tx *sql.Tx, schemaName string) error {
	query := fmt.Sprintf("CREATE SCHEMA IF NOT EXISTS %s;", schemaName)
	if _, err := tx.ExecContext(ctx, query); err != nil {
		return fmt.Errorf("failed to execute CREATE SCHEMA %s: %w", schemaName, err)
	}
	return nil
}

func (r *postgresProvisionerRepository) ExecuteMigrationTx(ctx context.Context, tx *sql.Tx, schemaName, migrationFilePath string) error {
	sqlBytes, err := os.ReadFile(migrationFilePath)
	if err != nil {
		return fmt.Errorf("failed to read migration file %s: %w", migrationFilePath, err)
	}

	migrationSQL := strings.ReplaceAll(string(sqlBytes), "{{SCHEMA_NAME}}", schemaName)
	if _, err := tx.ExecContext(ctx, migrationSQL); err != nil {
		return fmt.Errorf("failed to execute migration template for schema %s: %w", schemaName, err)
	}
	return nil
}

func (r *postgresProvisionerRepository) SeedOwnerMemberTx(ctx context.Context, tx *sql.Tx, schemaName, userID, name, email string) error {
	// ON CONFLICT (user_id) DO NOTHING makes this operation idempotent.
	// If the same UserRegistered event is delivered twice (e.g., after a DB crash
	// before the outbox could mark the row PUBLISHED), the second INSERT is a
	// safe no-op. The tenant_members table has UNIQUE(user_id) to back this up.
	query := fmt.Sprintf(`
		INSERT INTO %s.tenant_members (user_id, name, email, role)
		VALUES ($1, $2, $3, 'owner')
		ON CONFLICT (user_id) DO NOTHING;
	`, schemaName)
	if _, err := tx.ExecContext(ctx, query, userID, name, email); err != nil {
		return fmt.Errorf("failed to seed owner member into %s.tenant_members: %w", schemaName, err)
	}
	return nil
}
