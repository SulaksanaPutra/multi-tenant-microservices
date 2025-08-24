package repository

import (
	"context"
	"database/sql"
	"fmt"
)

type Tenant struct {
	ID      string
	Name    string
	Slug    string
	OwnerID string
}

type TenantRepository interface {
	CreateTenant(ctx context.Context, tx *sql.Tx, tenant Tenant) error
}

type postgresTenantRepository struct{}

func NewTenantRepository() TenantRepository {
	return &postgresTenantRepository{}
}

func (r *postgresTenantRepository) CreateTenant(ctx context.Context, tx *sql.Tx, tenant Tenant) error {
	query := `
		INSERT INTO public.tenants (id, name, slug, owner_id)
		VALUES ($1, $2, $3, $4);
	`
	if _, err := tx.ExecContext(ctx, query, tenant.ID, tenant.Name, tenant.Slug, tenant.OwnerID); err != nil {
		return fmt.Errorf("failed to insert tenant record into public.tenants: %w", err)
	}
	return nil
}
