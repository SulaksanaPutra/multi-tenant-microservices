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
		INSERT INTO public.user_credentials (user_id, email, password_hash, updated_at)
		VALUES ($1, $2, $3, NOW())
		ON CONFLICT (email) DO UPDATE
		SET password_hash = EXCLUDED.password_hash,
		    user_id       = EXCLUDED.user_id,
		    updated_at    = NOW();
	`
	if _, err := exec.ExecContext(ctx, query, input.UserID, input.Email, input.PasswordHash); err != nil {
		return fmt.Errorf("credential repository: failed to upsert credential for email='%s': %w", input.Email, err)
	}

	if input.TenantID != "" {
		if err := r.AddMembership(ctx, input.UserID, input.TenantID); err != nil {
			return err
		}
	}
	return nil
}

func (r *CredentialRepository) AddMembership(ctx context.Context, userID, tenantID string) error {
	exec := txcontext.GetExecutor(ctx, r.dbClient)
	query := `
		INSERT INTO public.user_tenant_memberships (user_id, tenant_id)
		VALUES ($1, $2)
		ON CONFLICT (user_id, tenant_id) DO NOTHING;
	`
	if _, err := exec.ExecContext(ctx, query, userID, tenantID); err != nil {
		return fmt.Errorf("credential repository: failed to add tenant membership user_id='%s' tenant_id='%s': %w", userID, tenantID, err)
	}
	return nil
}

func (r *CredentialRepository) ListUserMemberships(ctx context.Context, userID string) ([]string, error) {
	exec := txcontext.GetExecutor(ctx, r.dbClient)
	query := `
		SELECT tenant_id
		FROM public.user_tenant_memberships
		WHERE user_id = $1
		ORDER BY created_at ASC;
	`
	rows, err := exec.QueryContext(ctx, query, userID)
	if err != nil {
		return nil, fmt.Errorf("credential repository: failed to query user memberships: %w", err)
	}
	defer rows.Close()

	var tenantIDs []string
	for rows.Next() {
		var tid string
		if err := rows.Scan(&tid); err != nil {
			return nil, fmt.Errorf("credential repository: failed to scan membership tenant_id: %w", err)
		}
		tenantIDs = append(tenantIDs, tid)
	}
	return tenantIDs, nil
}

func (r *CredentialRepository) FindByEmail(ctx context.Context, email string) (*domain.Credential, error) {
	exec := txcontext.GetExecutor(ctx, r.dbClient)
	query := `
		SELECT user_id, email, password_hash, created_at, updated_at
		FROM public.user_credentials
		WHERE email = $1;
	`
	row := exec.QueryRowContext(ctx, query, email)
	return scanCredential(row)
}

func (r *CredentialRepository) FindByUserID(ctx context.Context, userID string) (*domain.Credential, error) {
	exec := txcontext.GetExecutor(ctx, r.dbClient)
	query := `
		SELECT user_id, email, password_hash, created_at, updated_at
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
