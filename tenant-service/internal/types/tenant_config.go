package types

import "database/sql"

// TenantConfig carries the resolved database connection pool and target schema metadata for a tenant.
type TenantConfig struct {
	TenantID      string
	PlacementType string
	DB            *sql.DB
	TargetSchema  string
}
