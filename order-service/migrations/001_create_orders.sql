-- Order table migration for order-service.
-- {{SCHEMA_NAME}} is substituted at runtime by the provisioner (shared or dedicated).

CREATE TABLE IF NOT EXISTS {{SCHEMA_NAME}}.orders (
    id          VARCHAR(36)    PRIMARY KEY,
    tenant_id   VARCHAR(36)    NOT NULL,
    customer_id VARCHAR(36)    NOT NULL,
    status      VARCHAR(50)    NOT NULL DEFAULT 'pending',
    currency    VARCHAR(3)     NOT NULL DEFAULT 'USD',
    quantity    INT            NOT NULL DEFAULT 1,
    price       NUMERIC(12, 2) NOT NULL DEFAULT 0,
    amount      NUMERIC(12, 2) NOT NULL DEFAULT 0,
    created_at  TIMESTAMPTZ    NOT NULL DEFAULT NOW(),
    updated_at  TIMESTAMPTZ    NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_orders_tenant_id ON {{SCHEMA_NAME}}.orders (tenant_id);
CREATE INDEX IF NOT EXISTS idx_orders_status    ON {{SCHEMA_NAME}}.orders (status);
