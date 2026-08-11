package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"auth-service/internal/domain"
	"auth-service/internal/infrastructure/postgres"
	"github.com/SulaksanaPutra/go-microservice-commons/txcontext"

	"github.com/lib/pq"
)

type RegisterPermissionItem struct {
	Name        string
	Description string
}

type PermissionRepository struct {
	dbClient *postgres.Client
}

func NewPermissionRepository(dbClient *postgres.Client) *PermissionRepository {
	return &PermissionRepository{dbClient: dbClient}
}

func (r *PermissionRepository) BulkUpsertPermissions(ctx context.Context, serviceName string, items []RegisterPermissionItem) error {
	if len(items) == 0 {
		return nil
	}
	exec := txcontext.GetExecutor(ctx, r.dbClient)

	query := `
		INSERT INTO public.permissions (id, name, service, description)
		VALUES ($1, $1, $2, $3)
		ON CONFLICT (id) DO UPDATE
		SET name        = EXCLUDED.name,
		    service     = EXCLUDED.service,
		    updated_at  = NOW(),
		    description = CASE WHEN EXCLUDED.description <> '' THEN EXCLUDED.description ELSE public.permissions.description END;
	`
	for _, item := range items {
		if _, err := exec.ExecContext(ctx, query, item.Name, serviceName, item.Description); err != nil {
			return fmt.Errorf("permission repository: failed to upsert permission '%s': %w", item.Name, err)
		}
	}
	return nil
}

func (r *PermissionRepository) ListAllPermissions(ctx context.Context) ([]domain.Permission, error) {
	exec := txcontext.GetExecutor(ctx, r.dbClient)
	query := `
		SELECT id, name, service, COALESCE(description, ''), created_at, updated_at
		FROM public.permissions
		ORDER BY service, name;
	`
	rows, err := exec.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("permission repository: failed to list permissions: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var permissions []domain.Permission
	for rows.Next() {
		var p domain.Permission
		if err := rows.Scan(&p.ID, &p.Name, &p.Service, &p.Description, &p.CreatedAt, &p.UpdatedAt); err != nil {
			return nil, fmt.Errorf("permission repository: failed to scan permission: %w", err)
		}
		permissions = append(permissions, p)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("permission repository: rows iteration error: %w", err)
	}
	return permissions, nil
}

func (r *PermissionRepository) FindByIDs(ctx context.Context, ids []string) ([]domain.Permission, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	exec := txcontext.GetExecutor(ctx, r.dbClient)
	query := `
		SELECT id, name, service, COALESCE(description, ''), created_at, updated_at
		FROM public.permissions
		WHERE id = ANY($1);
	`
	rows, err := exec.QueryContext(ctx, query, pq.Array(ids))
	if err != nil {
		return nil, fmt.Errorf("permission repository: failed to find permissions by ids: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var permissions []domain.Permission
	for rows.Next() {
		var p domain.Permission
		if err := rows.Scan(&p.ID, &p.Name, &p.Service, &p.Description, &p.CreatedAt, &p.UpdatedAt); err != nil {
			return nil, fmt.Errorf("permission repository: failed to scan permission: %w", err)
		}
		permissions = append(permissions, p)
	}
	return permissions, nil
}

func (r *PermissionRepository) FindByName(ctx context.Context, name string) (*domain.Permission, error) {
	exec := txcontext.GetExecutor(ctx, r.dbClient)
	query := `
		SELECT id, name, service, COALESCE(description, ''), created_at, updated_at
		FROM public.permissions
		WHERE name = $1;
	`
	var p domain.Permission
	if err := exec.QueryRowContext(ctx, query, name).Scan(&p.ID, &p.Name, &p.Service, &p.Description, &p.CreatedAt, &p.UpdatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, domain.ErrPermissionNotFound
		}
		return nil, fmt.Errorf("permission repository: failed to find permission by name '%s': %w", name, err)
	}
	return &p, nil
}
