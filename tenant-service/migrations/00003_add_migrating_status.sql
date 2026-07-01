-- +goose Up
-- Allow the MIGRATING lifecycle state so plan upgrades can freeze a tenant
-- while order-service performs the container/schema migration cutover.
ALTER TABLE public.tenants DROP CONSTRAINT tenants_status_check;
ALTER TABLE public.tenants ADD CONSTRAINT tenants_status_check CHECK (status IN ('pending', 'active', 'MIGRATING'));

-- +goose Down
ALTER TABLE public.tenants DROP CONSTRAINT tenants_status_check;
ALTER TABLE public.tenants ADD CONSTRAINT tenants_status_check CHECK (status IN ('pending', 'active'));
