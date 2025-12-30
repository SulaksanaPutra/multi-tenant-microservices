package repository

import (
	"context"
	"fmt"

	"user-service/internal/infrastructure/postgres"
	"user-service/internal/txcontext"
)

type CreateUserInput struct {
	ID    string
	Email string
	Name  string
}

type UserRepository struct {
	dbClient *postgres.Client
}

func NewUserRepository(dbClient *postgres.Client) *UserRepository {
	return &UserRepository{dbClient: dbClient}
}

func (r *UserRepository) CreateUser(ctx context.Context, input CreateUserInput) error {
	exec := txcontext.GetExecutor(ctx, r.dbClient)
	query := `
		INSERT INTO public.users (id, email, name)
		VALUES ($1, $2, $3);
	`
	if _, err := exec.ExecContext(ctx, query, input.ID, input.Email, input.Name); err != nil {
		return fmt.Errorf("failed to insert user record into public.users: %w", err)
	}
	return nil
}
