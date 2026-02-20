package main

import (
	"auth-service/internal/infrastructure/postgres"
)

// runMigrations applies the auth DB schema idempotently.
// It runs the DDL directly against the DB client to ensure tables exist on startup.
func runMigrations(dbClient *postgres.Client) error {
	ddl := `
		CREATE TABLE IF NOT EXISTS public.user_credentials (
			user_id       TEXT        NOT NULL,
			tenant_id     TEXT        NOT NULL DEFAULT '',
			email         TEXT        UNIQUE NOT NULL,
			password_hash TEXT        NOT NULL,
			created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			updated_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			PRIMARY KEY (user_id)
		);

		CREATE TABLE IF NOT EXISTS public.refresh_tokens (
			id         UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
			user_id    TEXT        NOT NULL,
			token_hash TEXT        UNIQUE NOT NULL,
			expires_at TIMESTAMPTZ NOT NULL,
			revoked_at TIMESTAMPTZ,
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
		);

		CREATE TABLE IF NOT EXISTS public.password_setup_tokens (
			id         UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
			user_id    TEXT        NOT NULL,
			tenant_id  TEXT        NOT NULL DEFAULT '',
			email      TEXT        NOT NULL,
			token_hash TEXT        UNIQUE NOT NULL,
			expires_at TIMESTAMPTZ NOT NULL,
			used_at    TIMESTAMPTZ,
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
		);

		CREATE INDEX IF NOT EXISTS idx_refresh_tokens_user_id     ON public.refresh_tokens(user_id);
		CREATE INDEX IF NOT EXISTS idx_refresh_tokens_token_hash   ON public.refresh_tokens(token_hash);
		CREATE INDEX IF NOT EXISTS idx_setup_tokens_token_hash     ON public.password_setup_tokens(token_hash);
		CREATE INDEX IF NOT EXISTS idx_setup_tokens_user_id        ON public.password_setup_tokens(user_id);
		CREATE INDEX IF NOT EXISTS idx_user_credentials_email      ON public.user_credentials(email);
		CREATE INDEX IF NOT EXISTS idx_user_credentials_user_id    ON public.user_credentials(user_id);
	`
	if _, err := dbClient.DB.Exec(ddl); err != nil {
		return err
	}
	return nil
}
