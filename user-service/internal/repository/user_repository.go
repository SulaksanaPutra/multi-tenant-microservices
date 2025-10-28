package repository

import (
	"context"
	"fmt"

	"user-service/internal/domain"
	"user-service/internal/infrastructure/postgres"
	"user-service/internal/txcontext"
)

type UserRepository struct {
	client *postgres.Client
}

func NewUserRepository(client *postgres.Client) *UserRepository {
	return &UserRepository{client: client}
}

func (r *UserRepository) CreateUser(ctx context.Context, user domain.User) error {
	exec := txcontext.GetExecutor(ctx, r.client.DB)
	query := `
		INSERT INTO public.users (id, email, name)
		VALUES ($1, $2, $3);
	`
	if _, err := exec.ExecContext(ctx, query, user.ID, user.Email, user.Name); err != nil {
		return fmt.Errorf("failed to insert user record into public.users: %w", err)
	}
	return nil
}
