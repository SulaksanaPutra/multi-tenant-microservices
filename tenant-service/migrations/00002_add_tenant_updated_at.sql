-- +goose Up
-- Expose updated_at on tenants so every resource surfaces created_at + updated_at.
ALTER TABLE public.tenants ADD COLUMN updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW();

-- +goose Down
ALTER TABLE public.tenants DROP COLUMN IF EXISTS updated_at;