package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/SulaksanaPutra/go-microservice-commons/txcontext"
	"tenant-service/internal/domain"
	"tenant-service/internal/infrastructure/postgres"
)

type UpsertServiceInfrastructureInput struct {
	TenantID    string
	ServiceName string
	DBHost      string
	DBPort      int
	DBName      string
	DBUser      string
	SchemaName  string
}

type TenantInfrastructureRepository struct {
	dbClient *postgres.Client
}

func NewTenantInfrastructureRepository(dbClient *postgres.Client) *TenantInfrastructureRepository {
	return &TenantInfrastructureRepository{dbClient: dbClient}
}

func (tenantInfrastructureRepository *TenantInfrastructureRepository) UpsertServiceInfrastructure(ctx context.Context, input UpsertServiceInfrastructureInput) error {
	exec := txcontext.GetExecutor(ctx, tenantInfrastructureRepository.dbClient)
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

func (tenantInfrastructureRepository *TenantInfrastructureRepository) CountPendingServices(ctx context.Context, tenantID string, requiredServices []string) (int, error) {
	if len(requiredServices) == 0 {
		return 0, nil
	}

	exec := txcontext.GetExecutor(ctx, tenantInfrastructureRepository.dbClient)

	placeholders := make([]string, len(requiredServices))
	args := make([]interface{}, len(requiredServices)+1)
	args[0] = tenantID
	for i, serviceName := range requiredServices {
		placeholders[i] = fmt.Sprintf("($%d)", i+2)
		args[i+1] = serviceName
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

func (tenantInfrastructureRepository *TenantInfrastructureRepository) FindByServiceName(ctx context.Context, tenantID, serviceName string) (*domain.TenantInfra, error) {
	exec := txcontext.GetExecutor(ctx, tenantInfrastructureRepository.dbClient)
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
			return nil, fmt.Errorf("no infrastructure registered for tenant '%s', service '%s': %w", tenantID, serviceName, domain.ErrNotFound)
		}
		return nil, fmt.Errorf("failed to query service infrastructure: %w", err)
	}
	return &res, nil
}
