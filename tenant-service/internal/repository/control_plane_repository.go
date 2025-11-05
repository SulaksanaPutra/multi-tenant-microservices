package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

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

type UpsertServiceInfraInput struct {
	TenantID    string
	ServiceName string
	DBHost      string
	DBPort      int
	DBName      string
	DBUser      string
	SchemaName  string
}

type ControlPlaneRepository struct {
	dbClient *postgres.Client
}

func NewControlPlaneRepository(dbClient *postgres.Client) *ControlPlaneRepository {
	return &ControlPlaneRepository{dbClient: dbClient}
}

func (r *ControlPlaneRepository) CreateTenant(ctx context.Context, input CreateTenantInput) error {
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

func (r *ControlPlaneRepository) UpsertServiceInfrastructure(ctx context.Context, input UpsertServiceInfraInput) error {
	exec := txcontext.GetExecutor(ctx, r.dbClient)
	const query = `
		INSERT INTO public.tenant_infrastructures (tenant_id, service_name, db_host, db_port, db_name, db_user, schema_name, checked_in_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, NOW())
		ON CONFLICT (tenant_id, service_name) DO UPDATE
		  SET db_host       = EXCLUDED.db_host,
		      db_port       = EXCLUDED.db_port,
		      db_name       = EXCLUDED.db_name,
		      db_user       = EXCLUDED.db_user,
		      schema_name   = EXCLUDED.schema_name,
		      checked_in_at = NOW();
	`
	if input.DBPort <= 0 {
		input.DBPort = 5432
	}
	if input.DBUser == "" {
		input.DBUser = "postgres"
	}

	_, err := exec.ExecContext(ctx, query,
		input.TenantID, input.ServiceName, input.DBHost, input.DBPort, input.DBName, input.DBUser, input.SchemaName,
	)
	if err != nil {
		return fmt.Errorf("failed to upsert service infrastructure for tenant '%s', service '%s': %w", input.TenantID, input.ServiceName, err)
	}
	return nil
}

func (r *ControlPlaneRepository) GetPendingServiceCount(ctx context.Context, tenantID string, requiredServices []string) (int, error) {
	if len(requiredServices) == 0 {
		return 0, nil
	}

	exec := txcontext.GetExecutor(ctx, r.dbClient)

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
			FROM public.tenant_infrastructures
			WHERE tenant_id = $1
		);
	`, strings.Join(placeholders, ", "))

	var count int
	if err := exec.QueryRowContext(ctx, query, args...).Scan(&count); err != nil {
		return 0, fmt.Errorf("failed to count pending services for tenant '%s': %w", tenantID, err)
	}
	return count, nil
}

func (r *ControlPlaneRepository) ActivateTenant(ctx context.Context, tenantID string) error {
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

func (r *ControlPlaneRepository) GetServiceInfrastructure(ctx context.Context, tenantID, serviceName string) (*domain.TenantInfra, error) {
	exec := txcontext.GetExecutor(ctx, r.dbClient)
	const query = `
		SELECT db_host, db_port, db_name, db_user, COALESCE(schema_name, '')
		FROM public.tenant_infrastructures
		WHERE tenant_id = $1 AND service_name = $2;
	`
	var res domain.TenantInfra
	res.TenantID = tenantID
	res.ServiceName = serviceName

	err := exec.QueryRowContext(ctx, query, tenantID, serviceName).Scan(
		&res.DBHost, &res.DBPort, &res.DBName, &res.DBUser, &res.SchemaName,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("no infrastructure registered for tenant '%s', service '%s'", tenantID, serviceName)
		}
		return nil, fmt.Errorf("failed to query service infrastructure: %w", err)
	}
	return &res, nil
}

func (r *ControlPlaneRepository) GetTenantByID(ctx context.Context, tenantID string) (*domain.Tenant, error) {
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
			return nil, fmt.Errorf("tenant '%s' not found", tenantID)
		}
		return nil, fmt.Errorf("failed to query tenant by ID: %w", err)
	}
	return &t, nil
}
