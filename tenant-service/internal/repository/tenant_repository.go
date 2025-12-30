package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"tenant-service/internal/domain"
	"tenant-service/internal/infrastructure/postgres"
	"tenant-service/internal/txcontext"
)

type CreateTenantInput struct {
	ID         string
	Name       string
	Slug       string
	OwnerEmail string
	OwnerName  string
	Plan       string
}

type TenantRepository struct {
	dbClient *postgres.Client
}

func NewTenantRepository(dbClient *postgres.Client) *TenantRepository {
	return &TenantRepository{dbClient: dbClient}
}

func (r *TenantRepository) CreateTenant(ctx context.Context, input CreateTenantInput) error {
	exec := txcontext.GetExecutor(ctx, r.dbClient)
	const query = `
		INSERT INTO public.tenants (id, name, slug, owner_email, owner_name, plan, status)
		VALUES ($1, $2, $3, $4, $5, $6, 'pending');
	`
	_, err := exec.ExecContext(ctx, query,
		input.ID, input.Name, input.Slug, input.OwnerEmail, input.OwnerName, input.Plan,
	)
	if err != nil {
		return fmt.Errorf("failed to insert tenant record: %w", err)
	}
	return nil
}

func (r *TenantRepository) ActivateTenant(ctx context.Context, tenantID string) error {
	exec := txcontext.GetExecutor(ctx, r.dbClient)
	const query = `
		UPDATE public.tenants SET status = 'active' WHERE id = $1;
	`
	_, err := exec.ExecContext(ctx, query, tenantID)
	if err != nil {
		return fmt.Errorf("failed to activate tenant '%s': %w", tenantID, err)
	}
	return nil
}

func (r *TenantRepository) GetTenantByID(ctx context.Context, tenantID string) (*domain.Tenant, error) {
	exec := txcontext.GetExecutor(ctx, r.dbClient)
	const query = `
		SELECT id, name, slug, owner_email, owner_name, plan, status, created_at
		FROM public.tenants
		WHERE id = $1;
	`
	var t domain.Tenant
	err := exec.QueryRowContext(ctx, query, tenantID).Scan(
		&t.ID, &t.Name, &t.Slug, &t.OwnerEmail, &t.OwnerName, &t.Plan, &t.Status, &t.CreatedAt,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("tenant '%s': %w", tenantID, domain.ErrNotFound)
		}
		return nil, fmt.Errorf("failed to query tenant by ID: %w", err)
	}
	return &t, nil
}
