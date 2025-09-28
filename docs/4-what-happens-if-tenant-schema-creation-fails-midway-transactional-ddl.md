# PostgreSQL Transactional DDL in Multi-Tenant Schema Provisioning

*Handling mid-execution failures, preventing orphaned tenant schemas, and transactional data definition.*

---

## 1. The Provisioning Flow

In our multi-tenant architecture, onboarding a new tenant involves provisioning dedicated schemas or databases:

```text
tenant-service ──► RegisterWorkspace ──► INSERT public.tenants (status='PENDING')
                         │
                         └──► Outbox: workspace.initiated
                                     │
                                     ▼
                              infra-provisioner
                                     │
                 ┌───────────────────┴───────────────────┐
                 ▼                                       ▼
        Shared Schema Mode                      Dedicated Container Mode
        (CREATE SCHEMA tenant_xxx)              (Docker API: Run PostgreSQL container)
                 │                                       │
                 └───────────────────┬───────────────────┘
                                     │
                                     ▼
                    RabbitMQ: tenant.order_db.ready
                                     │
                                     ▼
tenant-service ──► Updates public.tenant_infrastructures
                         │
                         └──► ActivateWorkspace (status='ACTIVE')
```

Because schema provisioning involves multiple DDL statements (creating schemas, creating tables, running migrations, and inserting seed data), a failure midway through the sequence must be handled cleanly.

---

## 2. Failure Mode: The Non-Transactional DDL Problem

In many database engines (such as MySQL or Oracle), DDL statements (`CREATE TABLE`, `CREATE SCHEMA`, `ALTER TABLE`) execute an implicit commit. They cannot be executed inside a rollback-capable transaction block.

If a provisioning script fails halfway through on such engines:

```text
1. CREATE SCHEMA tenant_abc;          --> Auto-committed to disk
2. CREATE TABLE tenant_abc.orders;   --> Auto-committed to disk
3. CREATE TABLE tenant_abc.payments; --> DISK OUT OF SPACE / SYNTAX ERROR!
   (Process aborts)
```

Because steps 1 and 2 cannot be rolled back, the database is left with a half-created, corrupted schema. Retrying the migration fails because `tenant_abc` already exists, but downstream queries fail because `payments` is missing. Cleaning this up usually requires manual database interventions.

---

## 3. PostgreSQL Transactional DDL

PostgreSQL handles DDL statements transactionally. You can wrap schema and table creation commands inside standard `BEGIN ... COMMIT` blocks:

```sql
BEGIN;

CREATE SCHEMA IF NOT EXISTS tenant_tenant123;

CREATE TABLE tenant_tenant123.orders (
    id VARCHAR(255) PRIMARY KEY,
    customer_id VARCHAR(255) NOT NULL,
    total_amount NUMERIC(12, 2) NOT NULL,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE tenant_tenant123.order_items (
    id VARCHAR(255) PRIMARY KEY,
    order_id VARCHAR(255) REFERENCES tenant_tenant123.orders(id),
    product_id VARCHAR(255) NOT NULL,
    price NUMERIC(12, 2) NOT NULL
);

-- If any statement above fails, the entire transaction rolls back cleanly:
COMMIT;
```

If an error occurs anywhere inside the block, rolling back the transaction completely removes the created schema, tables, and constraints from the PostgreSQL catalog. No orphaned schema fragments remain.

---

## 4. Implementation in `tenant-service`

In our codebase, tenant initialization scripts are structured to run within explicit database transactions:

```go
func (r *ProvisionerRepository) ProvisionTenantSchema(ctx context.Context, tenantSlug string) error {
    tx, err := r.db.BeginTx(ctx, nil)
    if err != nil {
        return fmt.Errorf("failed to begin schema transaction: %w", err)
    }
    defer tx.Rollback()

    // 1. Create isolated schema
    schemaQuery := fmt.Sprintf("CREATE SCHEMA IF NOT EXISTS %s;", pq.QuoteIdentifier(tenantSlug))
    if _, err := tx.ExecContext(ctx, schemaQuery); err != nil {
        return fmt.Errorf("failed to create schema: %w", err)
    }

    // 2. Execute table definitions inside the transaction
    if err := r.runTenantMigrations(ctx, tx, tenantSlug); err != nil {
        return fmt.Errorf("failed to apply migrations in schema: %w", err)
    }

    // 3. Commit atomically
    return tx.Commit()
}
```

### Identifier Sanitization
Because schema names cannot be passed as parameterized query placeholders (`$1`), we must validate and sanitize identifier names to prevent SQL injection:
- Tenant slugs are constrained to alphanumeric characters and underscores (`[a-z0-9_]`).
- Identifiers are wrapped using `pq.QuoteIdentifier` before string interpolation.

---

## 5. Non-Transactional Migration Directives (`-- tx: false`)

Certain PostgreSQL commands cannot run inside a transaction block. Examples include:
- `CREATE DATABASE`
- `CREATE INDEX CONCURRENTLY`
- `VACUUM`

To support these statements when running schema migrations, our migration engine inspects migration file headers for the directive `-- tx: false`. When present, the migration runner skips the transaction wrapper and executes statements directly against the connection pool, handling idempotency through `IF NOT EXISTS` clauses.

---

## 6. Architectural Invariants & Operational Trade-offs

- **Catalog Rollback Invariant**: All schema definitions execute within PostgreSQL transactions unless marked `-- tx: false`. Any failure rolls back cleanly without leaving orphaned schema remnants.
- **Identifier Validation**: Schema identifiers must be validated against strict alphanumeric whitelists before string interpolation into DDL queries.
- **DDL Lock Durations**: While PostgreSQL supports transactional DDL, DDL statements acquire `AccessExclusiveLock` on target tables. Migrations must be scoped to short-lived executions to avoid blocking concurrent transactions.
