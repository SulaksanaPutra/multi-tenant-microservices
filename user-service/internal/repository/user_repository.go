package repository

import (
	"context"
	"database/sql"
	"fmt"
)

type User struct {
	ID    string
	Email string
	Name  string
}

type UserRepository interface {
	CreateUser(ctx context.Context, tx *sql.Tx, user User) error
}

type postgresUserRepository struct{}

func NewUserRepository() UserRepository {
	return &postgresUserRepository{}
}

func (r *postgresUserRepository) CreateUser(ctx context.Context, tx *sql.Tx, user User) error {
	query := `
		INSERT INTO public.users (id, email, name)
		VALUES ($1, $2, $3);
	`
	if _, err := tx.ExecContext(ctx, query, user.ID, user.Email, user.Name); err != nil {
		return fmt.Errorf("failed to insert user record into public.users: %w", err)
	}
	return nil
}
