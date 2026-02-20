package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"auth-service/internal/domain"
	"auth-service/internal/infrastructure/postgres"
	"auth-service/internal/txcontext"
)

type UpsertCredentialInput struct {
	UserID       string
	TenantID     string
	Email        string
	PasswordHash string
}

type CredentialRepository struct {
	dbClient *postgres.Client
}

func NewCredentialRepository(dbClient *postgres.Client) *CredentialRepository {
	return &CredentialRepository{dbClient: dbClient}
}

func (r *CredentialRepository) UpsertCredential(ctx context.Context, input UpsertCredentialInput) error {
	exec := txcontext.GetExecutor(ctx, r.dbClient)
	query := `
		INSERT INTO public.user_credentials (user_id, tenant_id, email, password_hash, updated_at)
		VALUES ($1, $2, $3, $4, NOW())
		ON CONFLICT (email) DO UPDATE
		SET password_hash = EXCLUDED.password_hash,
		    user_id       = EXCLUDED.user_id,
		    tenant_id     = EXCLUDED.tenant_id,
		    updated_at    = NOW();
	`
	if _, err := exec.ExecContext(ctx, query, input.UserID, input.TenantID, input.Email, input.PasswordHash); err != nil {
		return fmt.Errorf("credential repository: failed to upsert credential for email='%s': %w", input.Email, err)
	}
	return nil
}

func (r *CredentialRepository) FindByEmail(ctx context.Context, email string) (*domain.Credential, error) {
	exec := txcontext.GetExecutor(ctx, r.dbClient)
	query := `
		SELECT user_id, tenant_id, email, password_hash, created_at, updated_at
		FROM public.user_credentials
		WHERE email = $1;
	`
	row := exec.QueryRowContext(ctx, query, email)
	return scanCredential(row)
}

func (r *CredentialRepository) FindByUserID(ctx context.Context, userID string) (*domain.Credential, error) {
	exec := txcontext.GetExecutor(ctx, r.dbClient)
	query := `
		SELECT user_id, tenant_id, email, password_hash, created_at, updated_at
		FROM public.user_credentials
		WHERE user_id = $1;
	`
	row := exec.QueryRowContext(ctx, query, userID)
	return scanCredential(row)
}

func scanCredential(row *sql.Row) (*domain.Credential, error) {
	var cred domain.Credential
	if err := row.Scan(
		&cred.UserID,
		&cred.TenantID,
		&cred.Email,
		&cred.PasswordHash,
		&cred.CreatedAt,
		&cred.UpdatedAt,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, domain.ErrCredentialNotFound
		}
		return nil, fmt.Errorf("credential repository: failed to scan credential row: %w", err)
	}
	return &cred, nil
}
