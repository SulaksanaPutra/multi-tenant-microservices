// Package migration applies the service's embedded goose migrations at boot.
package migration

import (
	"context"
	"database/sql"
	"fmt"

	"user-service/migrations"

	"github.com/pressly/goose/v3"
)

// Run applies all pending goose migrations; safe to call on every boot.
func Run(ctx context.Context, db *sql.DB) error {
	goose.SetBaseFS(migrations.FS)
	goose.SetLogger(goose.NopLogger())

	if err := goose.SetDialect("postgres"); err != nil {
		return fmt.Errorf("migration: failed to set postgres dialect: %w", err)
	}

	if err := goose.UpContext(ctx, db, "."); err != nil {
		return fmt.Errorf("migration: failed to apply pending migrations: %w", err)
	}

	return nil
}
