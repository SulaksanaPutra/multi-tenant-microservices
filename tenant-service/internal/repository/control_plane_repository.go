package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"tenant-service/internal/infrastructure/postgres"
)

type TenantMetadata struct {
	ID            string
	Name          string
	PlacementType string // "SHARED" or "DEDICATED"
	SchemaName    string // e.g. "tenant_123" (if SHARED)
	DbDSN         string // Connection string (if DEDICATED)
}

// ControlPlaneRepository handles tenant catalog metadata lookups on the main DB.
type ControlPlaneRepository interface {
	GetTenantMetadataBySlug(ctx context.Context, slug string) (*TenantMetadata, error)
}

type controlPlaneRepository struct {
	dbClient *postgres.Client
}

func NewControlPlaneRepository(dbClient *postgres.Client) ControlPlaneRepository {
	return &controlPlaneRepository{dbClient: dbClient}
}

func (r *controlPlaneRepository) GetTenantMetadataBySlug(ctx context.Context, slug string) (*TenantMetadata, error) {
	const query = `
		SELECT id, name, placement_type, COALESCE(schema_name, ''), COALESCE(db_dsn, '')
		FROM public.tenants
		WHERE slug = $1;
	`
	var meta TenantMetadata
	err := r.dbClient.QueryRowContext(ctx, query, slug).Scan(
		&meta.ID, &meta.Name, &meta.PlacementType, &meta.SchemaName, &meta.DbDSN,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("tenant with slug '%s' not found", slug)
		}
		return nil, fmt.Errorf("failed to query tenant metadata: %w", err)
	}
	return &meta, nil
}
