# What Happens If Tenant Schema Creation Fails Mid-Way? Transactional DDL in PostgreSQL

*Tracing Mid-Execution Crashes, Orphaned Tenant Data, and PostgreSQL Transactional DDL Superpowers*

---

## 1. The Provisioning Workflow: "Register Workspace, Provision Infrastructure, Activate"

When building the multi-tenant onboarding flow for my microservices project, `tenant-service` exposes a control-plane registry that tracks every workspace.

The onboarding flow is asynchronous and event-driven:

```
tenant-service    ──► [RegisterWorkspace] ──► INSERT public.tenants (status=pending)
                             │
                             └──► outbox ──► company.events ──► infra-provisioner (Docker API)
                                                                      │
                                      ProvisionDedicatedContainer / shared schema
                                                                      │
                                      company.events ◄── tenant.order_db.ready
                                                                      │
tenant-service ──► UPSERT public.tenant_infrastructures  (routing write-back)
                             │
                             └──► when all required services checked in
                                      └──► ActivateWorkspace (status=active)
                                               └──► workspace.ready ──► notification-service
```

Each step must be crash-safe. This doc traces what happens when any step fails mid-way.

---

## 2. Tracing the Path to Disaster: The Sequential Non-Tx Trap

What actually happens if these operations run sequentially without transaction wrappers?

When `tenant-service` receives a `RegisterWorkspace` request, it does two physical database writes:
1. `INSERT INTO public.tenants ...` (Control-plane registry row)
2. `INSERT INTO public.outbox ...` (`workspace.initiated` event for `infra-provisioner`)

> **"What happens if my Go app process gets killed or crashes right after Step 1, before Step 2 runs?"**

Tracing the execution revealed the reality of sequential execution:

1. **Step 1 is saved permanently**: Postgres executed `INSERT INTO public.tenants` as an independent SQL statement. It is written to disk immediately.
2. **Step 2 never runs**: Because the process crashed before Step 2, the `workspace.initiated` outbox row is never created.
3. **`infra-provisioner` stays idle**: RabbitMQ never gets the event, so no database schema / container is ever provisioned.
4. **The workspace is stranded as an Orphan**:
   - The `tenants` registry row exists on disk.
   - But the tenant's data-plane infrastructure was never provisioned.
   - The workspace stays `status=pending` forever.

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                       THE SEQUENTIAL NON-TX TRAP                            │
│                                                                             │
│  [1. INSERT public.tenants] ────► SUCCESS (Saved to DB disk)                │
│  ─────── 💥 PROCESS CRASHES AT THIS EXACT MILLISECOND ───────────────────── │
│  [2. INSERT public.outbox] ────► NEVER RUNS! (Event vanished)               │
│                                                                             │
│  RESULT: Orphan pending tenant + No Infrastructure + Broken Onboarding      │
└─────────────────────────────────────────────────────────────────────────────┘
```

---

## 3. PostgreSQL's Transactional DDL

To fix this, the control-plane writes must either commit together or not happen at all.

This is where PostgreSQL reveals its superpower: **Transactional DDL**.

Unlike MySQL—which forces an implicit commit on DDL statements like `CREATE SCHEMA` or `CREATE TABLE`—PostgreSQL allows wrapping DDL and DML inside a standard transaction block (`sql.Tx`). And because our schema is now owned by **goose migrations** (library-mode, applied at boot), every migration file runs inside a single transaction by default.

### 3a. Transactional DDL at the Database Level

PostgreSQL executes `CREATE TABLE`, `ALTER TABLE`, and `CREATE SCHEMA` inside a transaction. If the process crashes before `COMMIT`, PostgreSQL automatically triggers a **Rollback** and the DDL never lands.

### 3b. The goose Migration Safety Net

In this codebase every service runs embedded goose migrations on boot via `internal/migration/migrate.go`:

```go
// Package migration applies the service's embedded goose migrations at boot.
func Run(ctx context.Context, db *sql.DB) error {
    goose.SetBaseFS(migrations.FS)
    goose.SetDialect("postgres")
    return goose.UpContext(ctx, db, ".")
}
```

Each `-- +goose Up` block is a single transaction:

```sql
-- +goose Up
CREATE TABLE IF NOT EXISTS public.tenants (
    id           VARCHAR(36)  PRIMARY KEY,
    ...
    status       VARCHAR(50)  NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'active')),
    ...
);

-- +goose Down
DROP TABLE IF EXISTS public.tenants;
```

If the app crashes mid-migration, the transaction rolls back and the schema is **completely unchanged**. The next boot re-applies the migration cleanly.

---

## 4. The Provisioning Failure: What Happens Mid-Way Today?

The hard part of provisioning isn't the registry DDL — it's the **cross-service infrastructure step** handled by `infra-provisioner`.

### The Event Flow That Must Survive Crashes

```
tenant-service outbox worker ──► Publish workspace.initiated (company.events)
                                        │
                                        ▼
                        infra-provisioner (QoS=1 consumer)
                                        │
                        ProvisionDedicatedContainer / shared schema
                                        │  (Docker API call — not transactional)
                                        ▼
                        Publish tenant.order_db.ready (company.events)
                                        │
                                        ▼
                        tenant-service records tenant_infrastructures (UPSERT)
                                        │
                                        └──► ActivateWorkspace when all services ready
```

### What Happens If `infra-provisioner` Crashes After Creating the Container?

The `workspace.initiated` message is **NACKed and requeued** (see [workspace_initiated_consumer.go](../infra-provisioner/internal/consumer/workspace_initiated_consumer.go)):

```go
provEvent, err := c.handleProvisioning(ctx, evt)
if err != nil {
    _ = d.Nack(false, true) // requeue for retry
    return err
}
```

When the worker restarts, it re-provisions. The container provisioning is designed to be **idempotent** (`create if not exists`), so a duplicate provision does not corrupt state.

### What Happens If `tenant-service` Crashes Mid-Way?

The `tenant.order_db.ready` delivery is replayed. The consumer wraps its work in a transaction (see [tenant_ready_consumer.go](../tenant-service/internal/consumer/tenant_ready_consumer.go)):

1. **Inbox guard** (`ClaimEvent` on `public.inbox`) rejects the duplicate `event_id` if it already committed.
2. The `tenant_infrastructures` write is an **UPSERT** (`ON CONFLICT (tenant_id, service_name) DO UPDATE`), so replaying it is a no-op.
3. Only when **all required services** have checked in does `ActivateWorkspace` flip the tenant to `status=active` and stage `workspace.ready`.

### The Recovery Path Is Always the Same

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                    THE EVENT-DRIVEN SAFETY NET                              │
│                                                                             │
│  BEGIN TRANSACTION (consumer)                                               │
│  ├─ 1. INSERT INTO public.inbox (event_id)   (dedup barrier)                │
│  ├─ 2. UPSERT tenant_infrastructures         (idempotent)                   │
│  └─  PROCESS CRASHES BEFORE COMMIT                                          │
│                                                                             │
│  PostgreSQL Connection Dropped → AUTOMATIC ROLLBACK                         │
│  └─ No partial write. DB stays clean.                                       │
│                                                                             │
│  RabbitMQ Re-delivers tenant.order_db.ready → Consumer Processes Cleanly!   │
└─────────────────────────────────────────────────────────────────────────────┘
```

There is no orphaned half-state: a tenant is either `pending` (not all services checked in) or `active` (fully provisioned), never somewhere in between.

---

## 5. Production Code Implementation

### The Control-Plane Registry (goose-managed)

Inspect [00001_init_tenant_manager_schema.sql](../tenant-service/migrations/00001_init_tenant_manager_schema.sql):

```sql
-- +goose Up
CREATE TABLE IF NOT EXISTS public.tenants (
    id           VARCHAR(36)  PRIMARY KEY,
    name         VARCHAR(255) NOT NULL,
    slug         VARCHAR(255) NOT NULL,
    owner_email  VARCHAR(255) NOT NULL,
    owner_name   VARCHAR(255) NOT NULL,
    plan         VARCHAR(50)  NOT NULL CHECK (plan IN ('shared', 'dedicated')),
    status       VARCHAR(50)  NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'active')),
    created_at   TIMESTAMPTZ  NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS public.tenant_infrastructures (
    tenant_id     VARCHAR(36)  NOT NULL REFERENCES public.tenants(id) ON DELETE CASCADE,
    service_name  VARCHAR(100) NOT NULL,
    db_host       VARCHAR(255) NOT NULL,
    db_port       INT          NOT NULL DEFAULT 5432,
    db_name       VARCHAR(255) NOT NULL,
    db_user       VARCHAR(255) NOT NULL,
    schema_name   VARCHAR(255),
    checked_in_at TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    PRIMARY KEY (tenant_id, service_name)
);
-- +goose Down
DROP TABLE IF EXISTS public.tenant_infrastructures;
DROP TABLE IF EXISTS public.tenants;
```

### The Registration Flow in `workspace_service.go`

Inspect [workspace_service.go](../tenant-service/internal/service/workspace_service.go):

```go
func (workspaceService *WorkspaceService) RegisterWorkspace(ctx context.Context, input RegisterWorkspaceInput) (*RegisterWorkspaceOutput, error) {
    tenantID := domain.GenerateTenantID()
    outboxID := domain.GenerateOutboxID()

    evt := domain.WorkspaceInitiatedEvent{
        EventID:    outboxID,
        TenantID:   tenantID,
        Plan:       plan.String(),
        OwnerEmail: input.OwnerEmail,
        OwnerName:  input.OwnerName,
    }

    // 1. Create the registry row
    if err := workspaceService.tenantRepository.CreateTenant(ctx, repository.CreateTenantInput{...}); err != nil {
        return nil, fmt.Errorf("workspace service: failed to create tenant record: %w", err)
    }

    // 2. Stage the outbox event
    if err := workspaceService.outboxRepository.CreateOutboxMessage(ctx, repository.CreateOutboxMessageInput{
        ID:            outboxID,
        TenantID:      tenantID,
        AggregateType: "WORKSPACE",
        AggregateID:   tenantID,
        EventType:     "workspace.initiated",
        Payload:       payloadBytes,
    }); err != nil {
        return nil, fmt.Errorf("workspace service: failed to stage workspace.initiated outbox event: %w", err)
    }

    return &RegisterWorkspaceOutput{TenantID: tenantID, Status: "accepted"}, nil
}
```

### The Idempotent Infrastructure Write-Back

Inspect [tenant_infrastructure_repository.go](../tenant-service/internal/repository/tenant_infrastructure_repository.go):

```go
INSERT INTO public.tenant_infrastructures
    (tenant_id, service_name, db_host, db_port, db_name, db_user, schema_name)
VALUES ($1, $2, $3, $4, $5, $6, $7)
ON CONFLICT (tenant_id, service_name) DO UPDATE
    SET db_host = EXCLUDED.db_host, db_port = EXCLUDED.db_port, ...;
```

---

## 6. PostgreSQL vs. MySQL: Why Database Engine Choice Matters

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                      TRANSACTIONAL DDL COMPARISON                           │
├───────────────────────────────┬─────────────────────┬───────────────────────┤
│ FEATURE                       │ POSTGRESQL          │ MYSQL / INNODB        │
├───────────────────────────────┼─────────────────────┼───────────────────────┤
│ DDL Inside Transactions       │ Full Support        │ Implicit Commit       │
│ (`CREATE TABLE/SCHEMA`)       │                     │ (Ends TX immediately) │
├───────────────────────────────┼─────────────────────┼───────────────────────┤
│ Rollback on Crash             │ Drops Table & Data  │ Table Persists!       │
│                               │ (Restores Clean DB) │ (Orphaned Tables)     │
├───────────────────────────────┼─────────────────────┼───────────────────────┤
│ Safe Redelivery               │ Automatic & Clean   │ Requires Manual       │
│                               │                     │ Cleanup Script        │
└───────────────────────────────┴─────────────────────┴───────────────────────┘
```

In MySQL, issuing `CREATE TABLE` or `CREATE SCHEMA` automatically triggers an implicit `COMMIT`. If the app crashes mid-migration, MySQL **cannot** roll back the tables created so far.

In PostgreSQL, DDL statements are fully transactional. This is why our goose migrations run safely at boot: schema creation, registry rows, and inbox/outbox state all live and die within the same transaction boundary.

---

## 7. Summary

| Failure Point | Safety Mechanism |
|---------------|------------------|
| Migration crashes mid-DDL | goose wraps each `-- +goose Up` block in a Postgres transaction; rollback restores clean schema |
| `RegisterWorkspace` crashes between tenant row and outbox | Registry row stays `pending`; retry-safe because outbox + inbox are transactional |
| `infra-provisioner` crashes mid-provision | `workspace.initiated` is NACKed + requeued; provisioning is idempotent |
| Consumer crashes mid-processing | Inbox guard + UPSERT make redelivery a no-op |
| Workspace not fully provisioned | Status stays `pending` until ALL required services check in |

**The rule of thumb**: use PostgreSQL's transactional DDL for schema changes (they can never leave a half-applied state), and make every cross-service provisioning step idempotent so replays are harmless.
