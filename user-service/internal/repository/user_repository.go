package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"user-service/internal/domain"
	"user-service/internal/infrastructure/postgres"
	"user-service/internal/txcontext"
)

type CreateUserInput struct {
	ID    string
	Email string
	Name  string
}

type UpdateUserInput struct {
	ID   string
	Name string
}

type UserRepository struct {
	dbClient *postgres.Client
}

func NewUserRepository(dbClient *postgres.Client) *UserRepository {
	return &UserRepository{dbClient: dbClient}
}

func (userRepository *UserRepository) CreateUser(ctx context.Context, input CreateUserInput) error {
	exec := txcontext.GetExecutor(ctx, userRepository.dbClient)
	const query = `
		INSERT INTO public.users (id, email, name)
		VALUES ($1, $2, $3);
	`
	if _, err := exec.ExecContext(ctx, query, input.ID, input.Email, input.Name); err != nil {
		return fmt.Errorf("user repository: failed to insert user record into public.users: %w", err)
	}
	return nil
}

func (userRepository *UserRepository) AddUserTenantMembership(ctx context.Context, userID, tenantID string) error {
	exec := txcontext.GetExecutor(ctx, userRepository.dbClient)
	const query = `
		INSERT INTO public.user_tenant_memberships (user_id, tenant_id)
		VALUES ($1, $2)
		ON CONFLICT (user_id, tenant_id) DO NOTHING;
	`
	if _, err := exec.ExecContext(ctx, query, userID, tenantID); err != nil {
		return fmt.Errorf("user repository: failed to add tenant membership user_id='%s' tenant_id='%s': %w", userID, tenantID, err)
	}
	return nil
}

func (userRepository *UserRepository) GetUserByEmail(ctx context.Context, email string) (*domain.User, error) {
	exec := txcontext.GetExecutor(ctx, userRepository.dbClient)
	const query = `
		SELECT id, email, name, created_at, updated_at
		FROM public.users
		WHERE email = $1;
	`
	var user domain.User
	err := exec.QueryRowContext(ctx, query, email).Scan(&user.ID, &user.Email, &user.Name, &user.CreatedAt, &user.UpdatedAt)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("user email '%s': %w", email, domain.ErrNotFound)
		}
		return nil, fmt.Errorf("user repository: failed to query user by email: %w", err)
	}
	return &user, nil
}

func (userRepository *UserRepository) GetUserByID(ctx context.Context, userID string) (*domain.User, error) {
	if userID == "" {
		return nil, fmt.Errorf("user repository: user_id is required to fetch user")
	}
	exec := txcontext.GetExecutor(ctx, userRepository.dbClient)
	const query = `
		SELECT id, email, name, created_at, updated_at
		FROM public.users
		WHERE id = $1;
	`
	var user domain.User
	err := exec.QueryRowContext(ctx, query, userID).Scan(&user.ID, &user.Email, &user.Name, &user.CreatedAt, &user.UpdatedAt)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("user '%s': %w", userID, domain.ErrNotFound)
		}
		return nil, fmt.Errorf("user repository: failed to query user by id '%s': %w", userID, err)
	}
	return &user, nil
}

func (userRepository *UserRepository) UpdateUser(ctx context.Context, input UpdateUserInput) error {
	exec := txcontext.GetExecutor(ctx, userRepository.dbClient)
	const query = `
		UPDATE public.users
		SET name = $2, updated_at = NOW()
		WHERE id = $1;
	`
	res, err := exec.ExecContext(ctx, query, input.ID, input.Name)
	if err != nil {
		return fmt.Errorf("user repository: failed to update user record '%s': %w", input.ID, err)
	}
	rows, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("user repository: failed to get rows affected: %w", err)
	}
	if rows == 0 {
		return fmt.Errorf("user '%s': %w", input.ID, domain.ErrNotFound)
	}
	return nil
}

func (userRepository *UserRepository) ListUsers(ctx context.Context, tenantID string) ([]domain.User, error) {
	if tenantID == "" {
		return nil, errors.New("user repository: tenant_id is required to list users")
	}
	exec := txcontext.GetExecutor(ctx, userRepository.dbClient)
	const query = `
		SELECT u.id, u.email, u.name, u.created_at, u.updated_at
		FROM public.users u
		JOIN public.user_tenant_memberships m ON m.user_id = u.id
		WHERE m.tenant_id = $1
		ORDER BY u.created_at ASC;
	`
	rows, err := exec.QueryContext(ctx, query, tenantID)
	if err != nil {
		return nil, fmt.Errorf("user repository: failed to query users: %w", err)
	}
	defer rows.Close()

	var users []domain.User
	for rows.Next() {
		var user domain.User
		if err := rows.Scan(&user.ID, &user.Email, &user.Name, &user.CreatedAt, &user.UpdatedAt); err != nil {
			return nil, fmt.Errorf("user repository: failed to scan user row: %w", err)
		}
		users = append(users, user)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("user repository: rows iteration error: %w", err)
	}
	return users, nil
}
