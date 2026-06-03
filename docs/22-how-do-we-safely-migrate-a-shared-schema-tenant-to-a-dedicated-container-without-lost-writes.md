# How Do We Safely Migrate a Shared-Schema Tenant to a Dedicated Container Without Losing Writes?

"Read-Only Pause" Saga: the Transactional Outbox, the Fanout Freeze, the ALTER SCHEMA Lock, and the Compensating Rollback*

---

## 1. The Trap: This Looks Like a Simple Copy-and-Switch

A tenant on the **shared plan** stores its data in an isolated schema (`tenant_<id>_order_db`) inside the `shared_db` cluster. When that tenant upgrades to the **dedicated plan**, [infra-provisioner](..//infra-provisioner) provisions a private PostgreSQL Docker container and we must move every row out of the shared schema into the new container.

When I first scoped this, it looked like a simple "copy and switch" job: dump the schema, restore it into the new container, point routing at the new database, done. Then I started thinking about what else is running against that schema at the same time, and three properties of the system turned it into a trap:

1. **Live writes cannot be paused by time.** Orders keep arriving while the copy runs. If we start `pg_dump` while a transaction is mid-flight and it commits *after* the dump has passed that point, the new container is silently missing data — permanent, undetectable corruption.
2. **Routing is cached in memory.** Each `order-service` replica keeps a local `RoutingRegistry` mapping tenant → physical database. A replica that doesn't learn about the cutover will keep writing to the old shared schema, splitting writes across two locations.
3. **Background workers poll on their own schedule.** The `order-service` outbox worker publishes `order.created` events in batches. If it fires mid-migration, it may read from the schema while it is being renamed, or publish events whose payload references a database that no longer exists.

The plan proposed a four-phase **"Read-Only Pause"** strategy. This document records how the two latest commits implemented Phases 1, 2, 3, and 4, including the compensating rollback saga that makes the whole pipeline safe under RabbitMQ's *at-least-once* delivery semantics.

```text
[ Tenant Admin ]  PUT /api/tenants/me/plan  { plan: "dedicated" }
        │
        ▼
[ tenant-service ]  ChangeTenantPlan
        │  1. UPDATE tenants SET plan='dedicated'
        │  2. UPDATE tenants SET status='MIGRATING'          ── Phase 2
        │  3. Stage tenant.infrastructure_locking (fanout)   ── Phase 2
        │  4. Stage workspace.initiated                      ── Phase 3 trigger
        ▼
[ order-service replicas ]        [ infra-provisioner ]
   Lock consumer sets Status       workspace.initiated consumer
   = "MIGRATING" in RoutingRegistry    provisions dedicated container
        │                                │
        │   ┌────────────────────────────┤ ALTER SCHEMA <id>_order_db
        │   │  API: 423 Locked           │   RENAME TO ..._locked   (Phase 3)
        │   │  outbox worker: skips      │ pg_dump | sed | psql    (Phase 3)
        │   │  tenant entirely           ▼
        │   └───────────────────  infrastructure.provisioned
        │                                    │
        ▼                                    ▼
  [ order-service ] runs Goose migrations ──► tenant.order_db.ready
                                                  │
                                                  ▼
                                          [ tenant-service ]  updates routing
                                          table, status → ACTIVE, broadcast
                                          tenant.infrastructure_changed (Phase 4)
                                                  │
                                                  ▼
                                   order-service replicas purge lock, resume traffic
```

---

## 2. Phase 1: Building an Outbox Worth Pausing

Before we can *pause* background processes, there must be background processes worth pausing. `order-service` gained the same **transactional outbox** pattern already used by `user-service` and `tenant-service` (see [docs/3-what-if-the-database-crashes-after-rabbitmq-succeeds-the-idempotent-consumer.md](3-what-if-the-database-crashes-after-rabbitmq-succeeds-the-idempotent-consumer.md)).

### 2.1 The per-tenant `outbox` table

The migration `order-service/migrations/002_create_outbox.sql` uses the `{{SCHEMA_NAME}}` placeholder because `order-service` resolves its schema dynamically:

```sql
CREATE TABLE IF NOT EXISTS {{SCHEMA_NAME}}.outbox (
    id             VARCHAR(36)    PRIMARY KEY,
    tenant_id      VARCHAR(36)    NOT NULL,
    aggregate_type VARCHAR(100)   NOT NULL,
    aggregate_id   VARCHAR(36)    NOT NULL,
    event_type     VARCHAR(100)   NOT NULL,
    payload        TEXT           NOT NULL DEFAULT '{}',
    status         VARCHAR(20)    NOT NULL DEFAULT 'PENDING',
    retry_count    INT            NOT NULL DEFAULT 0,
    last_error     TEXT,
    claimed_at     TIMESTAMPTZ,
    next_retry_at  TIMESTAMPTZ,
    processed_at   TIMESTAMPTZ,
    created_at     TIMESTAMPTZ    NOT NULL DEFAULT NOW()
);
```

The repository (`order-service/internal/repository/outbox_repository.go`) is built around a `tenantdb.Config` rather than a fixed connection, so the same code targets `<tenant>_order_db.outbox` for shared-plan tenants and `public.outbox` in the dedicated container for dedicated-plan tenants.

### 2.2 A distributed, per-tenant worker

The first commit shipped a single-connection `OutboxWorker`; the second commit rewrote it to **iterate every tenant materialized in the local `RoutingRegistry`** (`order-service/internal/worker/outbox_worker.go`):

```go
func (w *OutboxWorker) forEachActiveTenant(ctx context.Context, fn func(cfg tenantdb.Config)) {
    for _, tenantID := range w.tenantLister.TenantIDs() {
        if w.routingStatus != nil && w.routingStatus.GetStatus(tenantID) == "MIGRATING" {
            log.Printf("OutboxWorker: Skipping tenant='%s'  tenant is MIGRATING (migration lock active).", tenantID)
            continue
        }
        cfg, err := w.resolver.GetTenantDB(ctx, tenantID)
        if err != nil { ... continue }
        fn(cfg)
    }
}
```

This is the migration-critical property: **the MIGRATING check happens *before* the `SELECT FOR UPDATE SKIP LOCKED` query is even executed.** A locked tenant is not merely skipped per-message; its outbox table is never touched at all. Polling (`5s` interval), `Poke()` debounced wake-ups, `RecoverStuckClaims`, `MarkPublished`, and exponential backoff `MarkFailed` all mirror the existing outbox workers.

### 2.3 The downstream consumer

`notification-service` gained an `OrderCreatedConsumer` (`notification-service/internal/consumer/order_created_consumer.go`) that consumes `order.created` on the `company.events` topic exchange and passes each event through the **transactional inbox guard** before any side effect:

```go
err := c.txManager.WithTransaction(ctx, func(txCtx context.Context) error {
    inboxInput := repository.CreateInboxMessageInput{
        EventID:   evt.EventID,
        TenantID:  evt.TenantID,
        EventType: domain.RoutingKeyOrderCreated,
        Payload:   d.Body,
    }
    isDup, err := c.inboxService.ClaimEvent(txCtx, inboxInput)
    if err != nil { return fmt.Errorf("inbox guard failed: %w", err) }
    if isDup { return nil } // duplicate → ACK, no side effect
    ...
})
```

Because the publisher stages `event_id` = the outbox row ID, a redelivered `order.created` (RabbitMQ *at-least-once*) is deduplicated at the inbox table — the exact rule the migration plan demanded for *every* new consumer.

---

## 3. Phase 2: The Fanout Freeze and the HTTP 423 Shield

### 3.1 The trigger: `ChangeTenantPlan`

`tenant-service/internal/service/workspace_service.go` now orchestrates the whole saga from a single HTTP call. When `PUT /api/tenants/me/plan` is invoked with `plan: "dedicated"`:

1. `UpdateTenantPlan` persists the new plan.
2. `SetTenantStatus(tenantID, "MIGRATING")` flips the tenant's status column.
3. It stages a **`tenant.infrastructure_locking`** outbox event (the global freeze).
4. It stages a **`workspace.initiated`** outbox event (the provisioner trigger), carrying `OwnerEmail`/`OwnerName` from the fresh `GetTenantByID` read.
5. `outboxWorker.Poke()` wakes the publisher immediately.

### 3.2 The Fanout Broadcast: every replica freezes

`order-service` gained an `InfrastructureLockingConsumer` (`order-service/internal/consumer/infrastructure_locking_consumer.go`). It binds to `company.events` with a **queue name of `""`** — an exclusive, server-assigned, auto-delete anonymous queue:

```go
q, err := c.client.Channel.QueueDeclare(
    "",    // empty string → server-assigned unique name (fanout broadcast pattern)
    false, // non-durable
    true,  // auto-delete when connection drops
    true,  // exclusive to this replica connection
    false, // no-wait
    nil,
)
if err := c.client.Channel.QueueBind(q.Name, domain.RoutingKeyInfrastructureLocking, domain.ExchangeCompanyEvents, false, nil); err != nil {
    return "", err
}
```

Because the queue is anonymous and exclusive, **every** live `order-service` replica receives its own copy of the lock event — no shared queue that would deliver to only one replica. On consumption it performs `routingRegistry.SetStatus(tenantID, "MIGRATING")`.

The lock is **sticky**: the consumer deliberately does *not* purge registry entries on reconnect, because `MIGRATING` must persist until an explicit unlock event (`tenant.infrastructure_changed`) arrives.

### 3.3 The API shield: HTTP 423

The `tenantDBResolver` (`order-service/internal/infrastructure/tenantdb/tenant_db_resolver.go`) returns `domain.ErrTenantMigrating` whenever the resolved metadata has `Status == "MIGRATING"`:

```go
if meta.Status == "MIGRATING" {
    return Config{}, domain.ErrTenantMigrating
}
```

The JWT middleware (`order-service/internal/middleware/jwt_middleware.go`) translates that into an immediate `HTTP 423 Locked`:

```go
tenantCfg, err := resolver.GetTenantDB(c.Request.Context(), tenantID)
if err != nil {
    if errors.Is(err, domain.ErrTenantMigrating) {
        httputil.WriteError(c, http.StatusLocked, "tenant infrastructure is locked for migration")
        c.Abort()
        return
    }
    ...
}
```

So while the migration is in flight, the tenant's API traffic is **globally paused** (423 for every write) and the outbox worker **never polls** that tenant's schema — without touching the authentication or permission layers.

---

## 4. Phase 3: The Absolute Lock and the `pg_dump | sed | psql` Pipeline

Phase 2 stops *new* work from entering the tenant. Phase 3 makes the copy itself deterministic by pushing the synchronization barrier down to PostgreSQL's ACID engine.

### 4.1 The absolute lock: `ALTER SCHEMA ... RENAME`

`infra-provisioner/internal/docker/migrator.go` implements `SchemaMigrator.LockSchema`, which renames the live schema to `tenant_<id>_order_db_locked` inside a transaction with a 15-second lock timeout:

```go
if _, err := tx.ExecContext(ctx, "SET lock_timeout = '15s';"); err != nil { ... }

alterQuery := fmt.Sprintf("ALTER SCHEMA %s RENAME TO %s;",
    pq.QuoteIdentifier(schemaName), pq.QuoteIdentifier(lockedSchemaName))
if _, err := tx.ExecContext(ctx, alterQuery); err != nil { ... }
```

`ALTER SCHEMA ... RENAME` requires an `ACCESS EXCLUSIVE` lock on the schema. Any in-flight transaction that still holds a reference to the old schema blocks the rename; if it takes longer than 15 seconds, the rename **fails with a timeout** — which is exactly the bloat detector the plan wanted. Identifiers are validated (`ValidateIdentifier`) and quote-doubled (`pq.QuoteIdentifier`) to prevent injection of malicious tenant IDs into DDL.

### 4.2 The data pipeline: `pg_dump | sed | psql`

`MigrateData` pipes three processes using Go's `os/exec`, rewriting the `_locked` schema to `public` on the fly:

```text
pg_dump -n <id>_order_db_locked --no-owner --no-acl --clean --if-exists
   │  (PGPASSWORD=sourcePass via cmd.Env)
   ▼
sed  s/ <id>_order_db_locked\./ public./g  (+ quoted + search_path variants)
   ▼
psql -h <dedicated-host> -d <dedicated-db>
   │  (PGPASSWORD=targetPass via cmd.Env)
```

Passwords are passed only through `cmd.Env = append(os.Environ(), "PGPASSWORD=...")` and the error buffer is sanitized (`sanitizeError`) against DSN-like patterns before logging, so secrets never leak into logs.

### 4.3 Idempotency under at-least-once delivery

The plan's "bulletproof additions" warned that an ack-loss redelivery could re-enter the migration branch after the lock already succeeded. The `workspace.initiated` consumer (`infra-provisioner/internal/consumer/workspace_initiated_consumer.go`) handles this explicitly:

- It checks for **both** the original schema *and* the `_locked` schema before deciding what to do.
- If `_locked` already exists, it **skips** `LockSchema` and logs *"resuming data migration pipeline"* — it never double-renames.
- `pg_dump` is invoked with `--clean --if-exists`, so re-applying the copy onto an already-partially-restored target is a safe drop-and-recreate.

This turns a redelivered `workspace.initiated` from a potential duplicate-disaster into a resume-with-no-op.

---

## 5. Phase 4: Cutover and the Unlock Broadcast

1. `infra-provisioner` publishes `infrastructure.provisioned` with the new container's host/port/db name.
2. `order-service`'s existing `InfrastructureProvisionedConsumer` consumes it, runs its Goose migrations (now loaded from the whole `migrations/` directory via `NewMigrationServiceFromDir`), updates the routing registry (with `Status: "active"`), evicts cached pools, and publishes `tenant.order_db.ready`.
3. `tenant-service`'s `TenantOrderDBReadyConsumer` (inbox-guarded) records the sanitized routing metadata into the `tenant_infrastructures` control-plane table.
4. On the **happy path**, `ActivateWorkspace` (or the ready-consumer's infrastructure update) sets status `ACTIVE` and broadcasts `tenant.infrastructure_changed` — see below.

### 5.1 The unlock broadcast

The unlock is a second fanout: `tenant.infrastructure_changed`. `ActivateWorkspace` stages it alongside `workspace.ready` so every `order-service` replica purges the locked registry entry and resumes traffic. Importantly, the code guards against a **phantom duplicate notification**:

```go
if tenant.Status != domain.StatusMigrating {
    // stage workspace.ready (welcome email)
}
// stage tenant.infrastructure_changed unconditionally
```

A tenant being cut over has already received its welcome email during initial activation; re-staging `workspace.ready` would make `notification-service` fire a duplicate email. The `infrastructure_changed` broadcast is still required to unfreeze the replicas.

### 5.2 The frontend

The frontend **short-polls** `GET /api/tenants/me` every 3–5 seconds until `status: ACTIVE`, then redirects to the dashboard. No websockets, no Server-Sent Events — the plan explicitly forbade them.

---

## 6. The Rollback Saga: `tenant.migration_failed`

If the deterministic lock times out or the copy fails, the system must restore the tenant to a consistent, unlocked state.

### 6.1 Provisioner-side compensation

In `workspace_initiated_consumer.go`, every failure path does three things, in order:

```go
_ = c.migrator.RestoreSchema(ctx, sharedDSN, lockedSchemaName, schemaName) // only for copy failure
_ = c.migrator.DestroyContainer(ctx, containerName)
_ = c.publisher.PublishTenantMigrationFailed(ctx, domain.TenantMigrationFailedEvent{
    EventID:  evt.EventID,
    TenantID: evt.TenantID,
    Reason:   err.Error(),
})
```

- **Lock timeout** (schema rename fails) → destroy the container, publish `tenant.migration_failed`. `pg_dump` is never started.
- **Copy failure** (pg_dump/psql/sed dies) → **first** `RestoreSchema` renames `_locked` back to the original name (so the shared schema is not permanently trapped under `_locked` and `order-service` doesn't crash with `schema does not exist`), **then** destroy the container, then publish the failure event.

`RestoreSchema` is the exact compensating action the plan's "Schema Rename Rollback" addition required.

### 6.2 Consumer-side compensation

`tenant-service` gained a `MigrationFailedConsumer` (`tenant-service/internal/consumer/migration_failed_consumer.go`) that runs the compensating saga inside a transaction:

1. `inboxService.ClaimEvent(eventID)` — idempotency barrier; a duplicate failure event is ACKed with no side effects.
2. `SetTenantStatus(tenantID, "active")` — flip the control-plane status back so the UI stops showing `MIGRATING`.
3. Stage a **`tenant.infrastructure_changed`** outbox event — so every `order-service` replica **purges the lock** and resumes serving traffic from the still-intact shared schema.

Transient transaction failures NACK with `requeue=true`; permanently bad JSON NACKs without requeue.

---

## 7. The State Machine at a Glance

```text
   pending ──► active ──► MIGRATING ──► active        (rollback: tenant.migration_failed)
                 ▲            │
                 │            ▼
                 │      (copy succeeds)
                 │            │
                 └────────────┘  active  (cutover + unlock broadcast)

  Events:  workspace.initiated        tenant.infrastructure_locking  (fanout freeze)
           infrastructure.provisioned tenant.infrastructure_changed (fanout unlock)
           tenant.order_db.ready      tenant.migration_failed       (compensation)
           order.created
```

---

## 8. What I Tested

- **`order-service` `InfrastructureLockingConsumer` test** — verifies a valid lock event sets `MIGRATING` in the registry, creates a minimal registry entry when the tenant is absent, ACKs, and NACKs bad JSON.
- **`order-service` `OutboxWorker` tests** (437 lines) — cover per-tenant iteration, MIGRATING-skip-before-poll, debounce/poke, stuck-claim recovery, publish success/failure, and full-batch re-poking.
- **`tenant-service` `MigrationFailedConsumer` test** — asserts the compensation resets status to `ACTIVE`, stages an `infrastructure_changed` outbox message, deduplicates a replayed `event_id`, NACKs-without-requeue on bad JSON, and NACKs-with-requeue on transient transaction failure.
- **`notification-service` `OrderCreatedConsumer` test** — exercises the inbox dedup guard and misrouted-routing-key discard.

---

## 9. Known Limitations & What I'd Do Next

- **`tenant-service` publishes from `ChangeTenantPlan` via the outbox**, so the lock broadcast and the provisioner trigger are durable but *async*. A tenant whose plan call succeeds is immediately `MIGRATING`, but there is a bounded window before replicas observe the freeze; the eventual `infrastructure_changed`/`migration_failed` event is what restores `ACTIVE`.
- **Status casing is not normalized.** The control-plane status constant is `"MIGRATING"` while `ACTIVE`/`PENDING` are lowercase (`"active"`, `"pending"`), and the registry/resolver compare against the literal `"MIGRATING"`. Any new status write must preserve that casing or the lock will be invisible.
- **`workspace.ready` is suppressed during migration** to avoid phantom duplicate emails, which is correct for plan upgrades but means the cutover relies solely on `infrastructure_changed` for replica unfreezing — if that broadcast is ever dropped from the outbox, replicas stay locked.
- **The old `shared_db` schema is not dropped.** After cutover, the `_locked` schema (or the still-locked copy) remains in the shared cluster as a deferred cleanup task, exactly as the plan specified — `DROP SCHEMA` is intentionally off the hot path.
- The commit that rewired the `OutboxWorker` to a per-tenant, resolver-driven loop is marked *unfinished*; the worker currently enumerates tenants from the local registry, so a replica that never materialized a tenant (e.g. via a direct outbox write) will not poll it until the registry learns about it.
