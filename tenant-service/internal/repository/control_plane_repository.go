package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"tenant-service/internal/infrastructure/postgres"
	"tenant-service/internal/txctx"
)

type TenantRecord struct {
	ID         string
	Name       string
	Slug       string
	OwnerEmail string
	OwnerName  string
	Plan       string
	Status     string
	CreatedAt  time.Time
}

type ServiceInfra struct {
	TenantID    string
	ServiceName string
	DSN         string
	SchemaName  string
}

type CreateTenantInput struct {
	ID         string
	Name       string
	Slug       string
	OwnerEmail string
	OwnerName  string
	Plan       string
}

// ControlPlaneRepository manages persistence for the central Control Plane registry.
type ControlPlaneRepository interface {
	CreateTenant(ctx context.Context, input CreateTenantInput) error
	UpsertServiceInfrastructure(ctx context.Context, tenantID, serviceName, dsn, schemaName string) error
	GetPendingServiceCount(ctx context.Context, tenantID string, requiredServices []string) (int, error)
	ActivateTenant(ctx context.Context, tenantID string) error
	GetServiceDSN(ctx context.Context, tenantID, serviceName string) (dsn string, schemaName string, err error)
	GetTenantByID(ctx context.Context, tenantID string) (*TenantRecord, error)
}

type controlPlaneRepository struct {
	dbClient *postgres.Client
}

func NewControlPlaneRepository(dbClient *postgres.Client) ControlPlaneRepository {
	return &controlPlaneRepository{dbClient: dbClient}
}

func (r *controlPlaneRepository) CreateTenant(ctx context.Context, input CreateTenantInput) error {
	exec := txctx.GetExecutor(ctx, r.dbClient)
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

func (r *controlPlaneRepository) UpsertServiceInfrastructure(ctx context.Context, tenantID, serviceName, dsn, schemaName string) error {
	exec := txctx.GetExecutor(ctx, r.dbClient)
	const query = `
		INSERT INTO public.tenant_services (tenant_id, service_name, dsn, schema_name, checked_in_at)
		VALUES ($1, $2, $3, $4, NOW())
		ON CONFLICT (tenant_id, service_name) DO UPDATE
		  SET dsn           = EXCLUDED.dsn,
		      schema_name   = EXCLUDED.schema_name,
		      checked_in_at = NOW();
	`
	_, err := exec.ExecContext(ctx, query, tenantID, serviceName, dsn, schemaName)
	if err != nil {
		return fmt.Errorf("failed to upsert service infrastructure for tenant '%s', service '%s': %w", tenantID, serviceName, err)
	}
	return nil
}

func (r *controlPlaneRepository) GetPendingServiceCount(ctx context.Context, tenantID string, requiredServices []string) (int, error) {
	if len(requiredServices) == 0 {
		return 0, nil
	}

	exec := txctx.GetExecutor(ctx, r.dbClient)

	placeholders := make([]string, len(requiredServices))
	args := make([]interface{}, len(requiredServices)+1)
	args[0] = tenantID
	for i, svc := range requiredServices {
		placeholders[i] = fmt.Sprintf("($%d)", i+2)
		args[i+1] = svc
	}

	query := fmt.Sprintf(`
		SELECT COUNT(*) FROM (
			VALUES %s
		) AS required(service_name)
		WHERE required.service_name NOT IN (
			SELECT service_name
			FROM public.tenant_services
			WHERE tenant_id = $1
		);
	`, strings.Join(placeholders, ", "))

	var count int
	if err := exec.QueryRowContext(ctx, query, args...).Scan(&count); err != nil {
		return 0, fmt.Errorf("failed to count pending services for tenant '%s': %w", tenantID, err)
	}
	return count, nil
}

func (r *controlPlaneRepository) ActivateTenant(ctx context.Context, tenantID string) error {
	exec := txctx.GetExecutor(ctx, r.dbClient)
	const query = `
		UPDATE public.tenants SET status = 'active' WHERE id = $1;
	`
	_, err := exec.ExecContext(ctx, query, tenantID)
	if err != nil {
		return fmt.Errorf("failed to activate tenant '%s': %w", tenantID, err)
	}
	return nil
}

func (r *controlPlaneRepository) GetServiceDSN(ctx context.Context, tenantID, serviceName string) (string, string, error) {
	exec := txctx.GetExecutor(ctx, r.dbClient)
	const query = `
		SELECT dsn, COALESCE(schema_name, '')
		FROM public.tenant_services
		WHERE tenant_id = $1 AND service_name = $2;
	`
	var dsn, schemaName string
	err := exec.QueryRowContext(ctx, query, tenantID, serviceName).Scan(&dsn, &schemaName)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", "", fmt.Errorf("no infrastructure registered for tenant '%s', service '%s'", tenantID, serviceName)
		}
		return "", "", fmt.Errorf("failed to query service DSN: %w", err)
	}
	return dsn, schemaName, nil
}

func (r *controlPlaneRepository) GetTenantByID(ctx context.Context, tenantID string) (*TenantRecord, error) {
	exec := txctx.GetExecutor(ctx, r.dbClient)
	const query = `
		SELECT id, name, slug, owner_email, owner_name, plan, status, created_at
		FROM public.tenants
		WHERE id = $1;
	`
	var t TenantRecord
	err := exec.QueryRowContext(ctx, query, tenantID).Scan(
		&t.ID, &t.Name, &t.Slug, &t.OwnerEmail, &t.OwnerName, &t.Plan, &t.Status, &t.CreatedAt,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("tenant '%s' not found", tenantID)
		}
		return nil, fmt.Errorf("failed to query tenant by ID: %w", err)
	}
	return &t, nil
}
