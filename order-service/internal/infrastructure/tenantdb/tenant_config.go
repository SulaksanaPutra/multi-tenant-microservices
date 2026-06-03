package tenantdb

import (
	"context"
	"database/sql"
)

// Config holds the resolved database connection handle, schema name, and tenant ID
// passed explicitly into per-request repository and service instances.
type Config struct {
	TenantID   string
	DB         *sql.DB
	SchemaName string
}

type configKey struct{}

// WithConfig returns a new Context carrying the tenantdb Config.
func WithConfig(ctx context.Context, cfg Config) context.Context {
	return context.WithValue(ctx, configKey{}, cfg)
}

// FromContext extracts Config from Context if present.
func FromContext(ctx context.Context) (Config, bool) {
	cfg, ok := ctx.Value(configKey{}).(Config)
	return cfg, ok
}


