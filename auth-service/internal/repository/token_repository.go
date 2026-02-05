package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"auth-service/internal/domain"
	"auth-service/internal/infrastructure"
	"auth-service/internal/txcontext"
)

// CreateRefreshTokenInput holds the fields required to persist a new refresh token.
type CreateRefreshTokenInput struct {
	UserID    string
	TokenHash string
	ExpiresAt time.Time
}

// TokenRepository performs SQL operations on the refresh_tokens table.
type TokenRepository struct {
	dbClient *infrastructure.Client
}

// NewTokenRepository constructs a TokenRepository backed by the auth DB.
func NewTokenRepository(dbClient *infrastructure.Client) *TokenRepository {
	return &TokenRepository{dbClient: dbClient}
}

// CreateRefreshToken inserts a new refresh token record.
func (r *TokenRepository) CreateRefreshToken(ctx context.Context, input CreateRefreshTokenInput) error {
	exec := txcontext.GetExecutor(ctx, r.dbClient)
	query := `
		INSERT INTO public.refresh_tokens (user_id, token_hash, expires_at)
		VALUES ($1, $2, $3);
	`
	if _, err := exec.ExecContext(ctx, query, input.UserID, input.TokenHash, input.ExpiresAt); err != nil {
		return fmt.Errorf("failed to insert refresh token for user_id='%s': %w", input.UserID, err)
	}
	return nil
}

// FindByTokenHash looks up a refresh token by its SHA-256 hash.
// Returns domain.ErrTokenNotFound if the token does not exist.
func (r *TokenRepository) FindByTokenHash(ctx context.Context, tokenHash string) (*domain.RefreshToken, error) {
	exec := txcontext.GetExecutor(ctx, r.dbClient)
	query := `
		SELECT id, user_id, token_hash, expires_at, revoked_at, created_at
		FROM public.refresh_tokens
		WHERE token_hash = $1;
	`
	row := exec.QueryRowContext(ctx, query, tokenHash)

	var rt domain.RefreshToken
	var revokedAt sql.NullTime
	if err := row.Scan(&rt.ID, &rt.UserID, &rt.TokenHash, &rt.ExpiresAt, &revokedAt, &rt.CreatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, domain.ErrTokenNotFound
		}
		return nil, fmt.Errorf("failed to query refresh token by hash: %w", err)
	}
	if revokedAt.Valid {
		rt.RevokedAt = &revokedAt.Time
	}
	return &rt, nil
}

// RevokeByTokenHash soft-revokes a refresh token by setting revoked_at = NOW().
// Returns domain.ErrTokenNotFound if the hash does not match any active record.
func (r *TokenRepository) RevokeByTokenHash(ctx context.Context, tokenHash string) error {
	exec := txcontext.GetExecutor(ctx, r.dbClient)
	query := `
		UPDATE public.refresh_tokens
		SET revoked_at = NOW()
		WHERE token_hash = $1 AND revoked_at IS NULL;
	`
	result, err := exec.ExecContext(ctx, query, tokenHash)
	if err != nil {
		return fmt.Errorf("failed to revoke refresh token: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to check affected rows during revoke: %w", err)
	}
	if rows == 0 {
		return domain.ErrTokenNotFound
	}
	return nil
}

// DeleteByTokenHash hard-deletes a refresh token record (used during token rotation).
func (r *TokenRepository) DeleteByTokenHash(ctx context.Context, tokenHash string) error {
	exec := txcontext.GetExecutor(ctx, r.dbClient)
	query := `DELETE FROM public.refresh_tokens WHERE token_hash = $1;`
	if _, err := exec.ExecContext(ctx, query, tokenHash); err != nil {
		return fmt.Errorf("failed to delete refresh token: %w", err)
	}
	return nil
}
