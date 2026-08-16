package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"auth-service/internal/domain"
	"auth-service/internal/infrastructure/postgres"
	"github.com/SulaksanaPutra/go-microservice-commons/txcontext"
)

type CreateSetupTokenInput struct {
	UserID    string
	TenantID  string
	Email     string
	TokenHash string
	ExpiresAt time.Time
}

type SetupTokenRepository struct {
	dbClient *postgres.Client
}

func NewSetupTokenRepository(dbClient *postgres.Client) *SetupTokenRepository {
	return &SetupTokenRepository{dbClient: dbClient}
}

func (setupTokenRepository *SetupTokenRepository) CreateSetupToken(ctx context.Context, input CreateSetupTokenInput) error {
	exec := txcontext.GetExecutor(ctx, setupTokenRepository.dbClient)
	query := `
		INSERT INTO public.password_setup_tokens (user_id, tenant_id, email, token_hash, expires_at)
		VALUES ($1, $2, $3, $4, $5);
	`
	if _, err := exec.ExecContext(ctx, query, input.UserID, input.TenantID, input.Email, input.TokenHash, input.ExpiresAt); err != nil {
		return fmt.Errorf("setup token repository: failed to create setup token: %w", err)
	}
	return nil
}

func (setupTokenRepository *SetupTokenRepository) FindByTokenHash(ctx context.Context, tokenHash string) (*domain.PasswordSetupToken, error) {
	exec := txcontext.GetExecutor(ctx, setupTokenRepository.dbClient)
	query := `
		SELECT id, user_id, tenant_id, email, token_hash, expires_at, used_at, created_at
		FROM public.password_setup_tokens
		WHERE token_hash = $1;
	`
	row := exec.QueryRowContext(ctx, query, tokenHash)
	var t domain.PasswordSetupToken
	var usedAt sql.NullTime

	if err := row.Scan(&t.ID, &t.UserID, &t.TenantID, &t.Email, &t.TokenHash, &t.ExpiresAt, &usedAt, &t.CreatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, domain.ErrTokenNotFound
		}
		return nil, fmt.Errorf("setup token repository: failed to scan token row: %w", err)
	}
	if usedAt.Valid {
		t.UsedAt = &usedAt.Time
	}
	return &t, nil
}

func (setupTokenRepository *SetupTokenRepository) MarkTokenUsed(ctx context.Context, tokenHash string) error {
	exec := txcontext.GetExecutor(ctx, setupTokenRepository.dbClient)
	query := `
		UPDATE public.password_setup_tokens
		SET used_at = NOW()
		WHERE token_hash = $1 AND used_at IS NULL;
	`
	res, err := exec.ExecContext(ctx, query, tokenHash)
	if err != nil {
		return fmt.Errorf("setup token repository: failed to mark token used: %w", err)
	}
	rows, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("setup token repository: failed to get rows affected: %w", err)
	}
	if rows == 0 {
		return domain.ErrTokenAlreadyUsed
	}
	return nil
}
