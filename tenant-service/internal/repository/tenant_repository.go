package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"tenant-service/internal/domain"
	"tenant-service/internal/infrastructure/postgres"

	"github.com/SulaksanaPutra/go-microservice-commons/txcontext"
)

type CreateTenantInput struct {
	ID         string
	Name       string
	Slug       string
	OwnerEmail string
	OwnerName  string
	Plan       string
}

type UpdateTenantInput struct {
	ID         string
	Name       string
	Slug       string
	OwnerEmail *string
	OwnerName  *string
}

type UpdateTenantPlanInput struct {
	ID   string
	Plan string
}

type TenantRepository struct {
	dbClient *postgres.Client
}

func NewTenantRepository(dbClient *postgres.Client) *TenantRepository {
	return &TenantRepository{dbClient: dbClient}
}

func (tenantRepository *TenantRepository) CreateTenant(ctx context.Context, input CreateTenantInput) error {
	exec := txcontext.GetExecutor(ctx, tenantRepository.dbClient)
	const query = `
		INSERT INTO public.tenants (id, name, slug, owner_email, owner_name, plan, status)
		VALUES ($1, $2, $3, $4, $5, $6, 'pending');
	`
	_, err := exec.ExecContext(ctx, query,
		input.ID, input.Name, input.Slug, input.OwnerEmail, input.OwnerName, input.Plan,
	)
	if err != nil {
		return fmt.Errorf("tenant repository: failed to insert tenant record: %w", err)
	}
	return nil
}

func (tenantRepository *TenantRepository) ActivateTenant(ctx context.Context, tenantID string) error {
	exec := txcontext.GetExecutor(ctx, tenantRepository.dbClient)
	const query = `
		UPDATE public.tenants SET status = 'active' WHERE id = $1;
	`
	_, err := exec.ExecContext(ctx, query, tenantID)
	if err != nil {
		return fmt.Errorf("tenant repository: failed to activate tenant '%s': %w", tenantID, err)
	}
	return nil
}

func (tenantRepository *TenantRepository) GetTenantByID(ctx context.Context, tenantID string) (*domain.Tenant, error) {
	exec := txcontext.GetExecutor(ctx, tenantRepository.dbClient)
	const query = `
		SELECT id, name, slug, owner_email, owner_name, plan, status, created_at, updated_at
		FROM public.tenants
		WHERE id = $1;
	`
	var t domain.Tenant
	err := exec.QueryRowContext(ctx, query, tenantID).Scan(
		&t.ID, &t.Name, &t.Slug, &t.OwnerEmail, &t.OwnerName, &t.Plan, &t.Status, &t.CreatedAt, &t.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("tenant '%s': %w", tenantID, domain.ErrNotFound)
		}
		return nil, fmt.Errorf("tenant repository: failed to query tenant by ID: %w", err)
	}
	return &t, nil
}

func (tenantRepository *TenantRepository) UpdateTenant(ctx context.Context, input UpdateTenantInput) error {
	exec := txcontext.GetExecutor(ctx, tenantRepository.dbClient)

	setClauses := []string{"name = $2", "slug = $3"}
	args := []any{input.ID, input.Name, input.Slug}
	nextParam := 4
	if input.OwnerEmail != nil {
		setClauses = append(setClauses, fmt.Sprintf("owner_email = $%d", nextParam))
		args = append(args, *input.OwnerEmail)
		nextParam++
	}
	if input.OwnerName != nil {
		setClauses = append(setClauses, fmt.Sprintf("owner_name = $%d", nextParam))
		args = append(args, *input.OwnerName)
	}

	query := fmt.Sprintf("UPDATE public.tenants SET updated_at = NOW(), %s WHERE id = $1;", strings.Join(setClauses, ", "))
	res, err := exec.ExecContext(ctx, query, args...)
	if err != nil {
		return fmt.Errorf("tenant repository: failed to update tenant '%s': %w", input.ID, err)
	}
	rows, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("tenant repository: failed to get rows affected: %w", err)
	}
	if rows == 0 {
		return fmt.Errorf("tenant '%s': %w", input.ID, domain.ErrNotFound)
	}
	return nil
}

func (tenantRepository *TenantRepository) UpdateTenantPlan(ctx context.Context, input UpdateTenantPlanInput) error {
	exec := txcontext.GetExecutor(ctx, tenantRepository.dbClient)
	const query = `
		UPDATE public.tenants
		SET plan = $2, updated_at = NOW()
		WHERE id = $1;
	`
	res, err := exec.ExecContext(ctx, query, input.ID, input.Plan)
	if err != nil {
		return fmt.Errorf("tenant repository: failed to update tenant plan for '%s': %w", input.ID, err)
	}
	rows, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("tenant repository: failed to get rows affected: %w", err)
	}
	if rows == 0 {
		return fmt.Errorf("tenant '%s': %w", input.ID, domain.ErrNotFound)
	}
	return nil
}

func (tenantRepository *TenantRepository) SetTenantStatus(ctx context.Context, tenantID, status string) error {
	exec := txcontext.GetExecutor(ctx, tenantRepository.dbClient)
	const query = `
		UPDATE public.tenants
		SET status = $2
		WHERE id = $1;
	`
	res, err := exec.ExecContext(ctx, query, tenantID, status)
	if err != nil {
		return fmt.Errorf("tenant repository: failed to set status for tenant '%s': %w", tenantID, err)
	}
	rows, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("tenant repository: failed to get rows affected: %w", err)
	}
	if rows == 0 {
		return fmt.Errorf("tenant '%s': %w", tenantID, domain.ErrNotFound)
	}
	return nil
}
