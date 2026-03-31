package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"auth-service/internal/domain"
	"auth-service/internal/infrastructure/postgres"
	"auth-service/internal/txcontext"
)

type CreateRefreshTokenInput struct {
	UserID    string
	TenantID  string
	TokenHash string
	ExpiresAt time.Time
}

type RevokeRefreshTokenInput struct {
	TokenHash string
}

type DeleteRefreshTokenInput struct {
	TokenHash string
}

type TokenRepository struct {
	dbClient *postgres.Client
}

func NewTokenRepository(dbClient *postgres.Client) *TokenRepository {
	return &TokenRepository{dbClient: dbClient}
}

func (r *TokenRepository) CreateRefreshToken(ctx context.Context, input CreateRefreshTokenInput) error {
	exec := txcontext.GetExecutor(ctx, r.dbClient)
	query := `
		INSERT INTO public.refresh_tokens (user_id, tenant_id, token_hash, expires_at)
		VALUES ($1, $2, $3, $4);
	`
	if _, err := exec.ExecContext(ctx, query, input.UserID, input.TenantID, input.TokenHash, input.ExpiresAt); err != nil {
		return fmt.Errorf("token repository: failed to insert refresh token for user_id='%s': %w", input.UserID, err)
	}
	return nil
}

func (r *TokenRepository) FindByTokenHash(ctx context.Context, tokenHash string) (*domain.RefreshToken, error) {
	exec := txcontext.GetExecutor(ctx, r.dbClient)
	query := `
		SELECT id, user_id, tenant_id, token_hash, expires_at, revoked_at, created_at
		FROM public.refresh_tokens
		WHERE token_hash = $1;
	`
	row := exec.QueryRowContext(ctx, query, tokenHash)

	var rt domain.RefreshToken
	var revokedAt sql.NullTime
	if err := row.Scan(&rt.ID, &rt.UserID, &rt.TenantID, &rt.TokenHash, &rt.ExpiresAt, &revokedAt, &rt.CreatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, domain.ErrTokenNotFound
		}
		return nil, fmt.Errorf("token repository: failed to query refresh token by hash: %w", err)
	}
	if revokedAt.Valid {
		rt.RevokedAt = &revokedAt.Time
	}
	return &rt, nil
}

func (r *TokenRepository) RevokeRefreshToken(ctx context.Context, input RevokeRefreshTokenInput) error {
	exec := txcontext.GetExecutor(ctx, r.dbClient)
	query := `
		UPDATE public.refresh_tokens
		SET revoked_at = NOW()
		WHERE token_hash = $1 AND revoked_at IS NULL;
	`
	result, err := exec.ExecContext(ctx, query, input.TokenHash)
	if err != nil {
		return fmt.Errorf("token repository: failed to revoke refresh token: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("token repository: failed to check affected rows: %w", err)
	}
	if rows == 0 {
		return domain.ErrTokenNotFound
	}
	return nil
}

func (r *TokenRepository) DeleteRefreshToken(ctx context.Context, input DeleteRefreshTokenInput) error {
	exec := txcontext.GetExecutor(ctx, r.dbClient)
	query := `DELETE FROM public.refresh_tokens WHERE token_hash = $1;`
	if _, err := exec.ExecContext(ctx, query, input.TokenHash); err != nil {
		return fmt.Errorf("token repository: failed to delete refresh token: %w", err)
	}
	return nil
}
