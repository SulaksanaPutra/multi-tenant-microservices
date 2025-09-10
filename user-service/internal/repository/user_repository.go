package repository

import (
	"context"
	"fmt"

	"user-service/internal/infrastructure/postgres"
	"user-service/internal/txctx"
)

type User struct {
	ID    string
	Email string
	Name  string
}

type UserRepository interface {
	CreateUser(ctx context.Context, user User) error
}

type userRepository struct {
	client *postgres.Client
}

func NewUserRepository(client *postgres.Client) UserRepository {
	return &userRepository{client: client}
}

func (r *userRepository) CreateUser(ctx context.Context, user User) error {
	exec := txctx.GetExecutor(ctx, r.client.DB)
	query := `
		INSERT INTO public.users (id, email, name)
		VALUES ($1, $2, $3);
	`
	if _, err := exec.ExecContext(ctx, query, user.ID, user.Email, user.Name); err != nil {
		return fmt.Errorf("failed to insert user record into public.users: %w", err)
	}
	return nil
}
