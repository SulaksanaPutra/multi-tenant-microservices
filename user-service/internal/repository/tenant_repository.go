package repository

import (
	"context"
	"fmt"

	"user-service/internal/infrastructure/postgres"
	"user-service/internal/txctx"
)

type Tenant struct {
	ID      string
	Name    string
	Slug    string
	OwnerID string
}

type TenantRepository interface {
	CreateTenant(ctx context.Context, tenant Tenant) error
	UpdateTenantPlacement(ctx context.Context, tenantID, placementType, schemaName, dbDSN string) error
}

type tenantRepository struct {
	client *postgres.Client
}

func NewTenantRepository(client *postgres.Client) TenantRepository {
	return &tenantRepository{client: client}
}

func (r *tenantRepository) CreateTenant(ctx context.Context, tenant Tenant) error {
	exec := txctx.GetExecutor(ctx, r.client.DB)
	query := `
		INSERT INTO public.tenants (id, name, slug, owner_id)
		VALUES ($1, $2, $3, $4);
	`
	if _, err := exec.ExecContext(ctx, query, tenant.ID, tenant.Name, tenant.Slug, tenant.OwnerID); err != nil {
		return fmt.Errorf("failed to insert tenant record into public.tenants: %w", err)
	}
	return nil
}

func (r *tenantRepository) UpdateTenantPlacement(ctx context.Context, tenantID, placementType, schemaName, dbDSN string) error {
	exec := txctx.GetExecutor(ctx, r.client.DB)
	query := `
		UPDATE public.tenants
		SET placement_type = COALESCE(NULLIF($2, ''), placement_type),
		    schema_name    = COALESCE(NULLIF($3, ''), schema_name),
		    db_dsn         = COALESCE(NULLIF($4, ''), db_dsn),
		    updated_at     = CURRENT_TIMESTAMP
		WHERE id = $1 OR slug = $1;
	`
	if _, err := exec.ExecContext(ctx, query, tenantID, placementType, schemaName, dbDSN); err != nil {
		return fmt.Errorf("failed to update tenant placement for '%s': %w", tenantID, err)
	}
	return nil
}
