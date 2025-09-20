# How Do We Decouple Control Plane & Data Plane? Workspace-First B2B Architecture

*An Engineering Deep Dive into Building a B2B SaaS Architecture with Passive Control Plane Registry, Autonomous Data Plane Provisioning, Deterministic Docker Idempotency, and Zero-Boot Connection Pools in Go*

---

## 1. The Monolithic Registration Bottleneck

In early SaaS architectures, tenant onboarding is often designed around a **User-First** model. A client sends a request to create a user, and during that single HTTP request or queue cycle, a single service attempts to:
1. Create the User identity.
2. Create the Tenant metadata.
3. Provision the tenant's database schema or container.
4. Seed default tenant settings and initial members.
5. Send a welcome email.

### Why This Fails at Scale
As a multi-tenant platform grows to support multiple microservices (`user-service`, `order-service`, `inventory-service`, `billing-service`), centralizing provisioning inside a single "monolithic" provisioner service creates severe architectural flaws:

1. **Loss of Bounded Context & Domain Autonomy**: If `tenant-service` provisions database schemas for `order-service`, then `tenant-service` MUST own `order-service`'s SQL DDL migration files (`001_create_orders.sql`). Whenever the Order team adds a new column, they must deploy `tenant-service`.
2. **Coupling to Storage Dialects**: `tenant-service` becomes a bloated "God Service" that needs database drivers, Docker SDKs, and configuration logic for PostgreSQL, MongoDB, ClickHouse, or whatever database engine any downstream domain service chooses to use.
3. **Synchronous Cascade Failures**: If one step fails (e.g. database schema creation takes 5 seconds or SMTP times out), the whole registration transaction fails or leaves orphaned resources behind.

---

## 2. The Architectural Paradigm: Workspace-First B2B Duality

To solve this, we refactored `microservice-api` into a **Workspace-First B2B SaaS Architecture** with a strict separation between the **Control Plane** and the **Data Plane**.

In a B2B SaaS model, customers sign up as an *Organization / Workspace* that users belong to.

```
                              ┌───────────────────────────────────┐
                              │          CLIENT REQUEST           │
                              │       POST /api/register          │
                              └─────────────────┬─────────────────┘
                                                │
                                                ▼
                              ┌───────────────────────────────────┐
                              │          TENANT-SERVICE           │
                              │      (Passive Control Plane)      │
                              └─────────────────┬─────────────────┘
                                                │
                                     WorkspaceInitiated Event
                                                │
                 ┌──────────────────────────────┴──────────────────────────────┐
                 ▼                                                             ▼
  ┌─────────────────────────────┐                               ┌─────────────────────────────┐
  │        USER-SERVICE         │                               │        ORDER-SERVICE        │
  │    (Identity Data Plane)    │                               │     (Domain Data Plane)     │
  └──────────────┬──────────────┘                               └──────────────┬──────────────┘
                 │                                                             │
        Creates User Identity                                          Provisions DB Schema
                 │                                                     or Docker Container
                 │                                                             │
                 │                                                PATCH /infrastructure
                 │                                                (Report DSN & Schema)
                 │                                                             │
                 └──────────────────────────────┬──────────────────────────────┘
                                                ▼
                              ┌───────────────────────────────────┐
                              │          TENANT-SERVICE           │
                              │       (Aggregates State)          │
                              └─────────────────┬─────────────────┘
                                                │
                                       WorkspaceReady Event
                                                │
                                                ▼
                              ┌───────────────────────────────────┐
                              │       NOTIFICATION-SERVICE        │
                              │     (Sends Welcome Email)         │
                              └───────────────────────────────────┘
```

---

## 3. The Control Plane Registry (`tenant-service`)

`tenant-service` pivots from an active provisioner into a **Passive Control Plane Registry & State Aggregator**.

### 1. Central Directory Schema (`tenantManagerDB`)
`tenant-service` owns `tenant_manager_db`, storing tenant state and domain service infrastructure write-backs:

```sql
-- public.tenants: The central registry row per workspace
CREATE TABLE IF NOT EXISTS public.tenants (
    id           VARCHAR(36)  PRIMARY KEY,
    name         VARCHAR(255) NOT NULL,
    slug         VARCHAR(255) NOT NULL UNIQUE,
    owner_email  VARCHAR(255) NOT NULL,
    owner_name   VARCHAR(255) NOT NULL,
    plan         VARCHAR(50)  NOT NULL CHECK (plan IN ('shared', 'dedicated')),
    status       VARCHAR(50)  NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'active')),
    created_at   TIMESTAMPTZ  NOT NULL DEFAULT NOW()
);

-- public.tenant_services: Domain service infrastructure write-backs
CREATE TABLE IF NOT EXISTS public.tenant_services (
    tenant_id     VARCHAR(36)  NOT NULL REFERENCES public.tenants(id) ON DELETE CASCADE,
    service_name  VARCHAR(100) NOT NULL, -- e.g. 'order-service'
    dsn           TEXT         NOT NULL,
    schema_name   VARCHAR(255),
    checked_in_at TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    PRIMARY KEY (tenant_id, service_name)
);
```

### 2. Registration Flow (Atomic Transaction)
When a client hits `POST /api/register`:
1. `tenant-service` generates a unique `tenantID` and `outboxID`.
2. Inside an atomic database transaction (`TxManager`), it inserts the tenant row with `status = 'pending'` and writes a `workspace.initiated` event to `public.outbox`.
3. Returns `202 Accepted` immediately to the client.

### 3. State Aggregation & `WorkspaceReady` Event
When domain services complete infrastructure setup, they send an internal write-back HTTP request (`PATCH /internal/tenants/:tenant_id/infrastructure`).

`tenant-service` upserts the row into `tenant_services` and checks if all required services have checked in:
```go
pendingCount, err := s.controlRepository.GetPendingServiceCount(ctx, input.TenantID, requiredServices)
if pendingCount == 0 {
    // All required domain services have provisioned their DBs!
    // Atomically activate workspace status & stage WorkspaceReady event
    s.controlRepository.ActivateTenant(ctx, input.TenantID)
    s.controlRepository.CreateOutboxMessage(ctx, workspaceReadyMsg)
}
```
Only when the workspace is `active` is `workspace.ready` emitted to `notification-service`.

---

## 4. Autonomous Data Plane Provisioning (`order-service`)

`order-service` owns its own data provisioning logic without leaking its database scripts or engine requirements to `tenant-service`.

### 1. Dual-Placement Strategy
When `order-service` consumes `workspace.initiated`:
* **Shared Plan**: `order-service` connects dynamically to the `sharedDB` host, executes `CREATE SCHEMA IF NOT EXISTS <tenantID>_order_db;`, and runs its own `001_create_orders.sql` migration script.
* **Dedicated Plan**: `order-service` connects dynamically to the PostgreSQL host, executes `CREATE DATABASE <tenantID>_order_db;`, and runs `001_create_orders.sql` inside it.

### 2. Deterministic Idempotency & The Docker Orphan Problem
If the network drops after container creation but before the write-back HTTP call completes, RabbitMQ redelivers the event. Without idempotency, retries would spawn duplicate Docker containers and leak host ports.

We solve this using **Deterministic Idempotency** via `CheckOrCreate`:

```go
func (d *DockerClient) CheckOrCreate(ctx context.Context, tenantID string) (string, error) {
    containerName := "dedicated_order_db_" + tenantID

    // 1. Inspect Docker for existing container by name filter
    existingPort, err := d.findContainerPort(ctx, containerName)
    if existingPort != "" {
        log.Printf("Container '%s' already exists on port %s. Reusing.", containerName, existingPort)
        return existingPort, nil // Skip docker run, reuse existing port
    }

    // 2. First-run: create & start container
    hostPort, err := d.createAndStart(ctx, containerName, tenantID)
    return hostPort, nil
}
```
This guarantees that background worker retries are infinitely safe and idempotent.

---

## 5. Zero-Boot Connections & The Pool Registry Pattern

### 1. Zero Persistent DB Handles at Startup
`order-service` does NOT hold a static database connection pool at `main()` boot time. It treats database connections as **100% dynamic resources**:
* **Provisioning Time**: Opens an on-demand temporary connection to `sharedDB` or dedicated container, runs DDL/migrations, and immediately closes the handle (`defer db.Close()`).
* **Runtime Query Time**: Opens tenant connections lazily on demand.

### 2. Thread-Safe `PoolRegistry` with TTL Eviction
To execute queries (`GET /api/orders`), `order-service` uses a `PoolRegistry` backed by `sync.RWMutex`:

```go
type poolEntry struct {
    db       *sql.DB
    lastUsed time.Time
}

type PoolRegistry struct {
    mu      sync.RWMutex
    entries map[string]*poolEntry
    ttl     time.Duration // 15 minutes
}
```

#### Cache Miss Resolution:
When a query hits `order-service` for `tenantID`:
1. **Read Lock Check**: If cached in `PoolRegistry`, update `lastUsed` and return `*sql.DB` instantly (0ms latency).
2. **Cache Miss**: `order-service` makes a fast internal HTTP call (`GET /internal/tenants/:tenant_id/infrastructure/order-service`) to `tenant-service` to retrieve the registered DSN.
3. **Connection Limits**: Opens the connection pool with strict per-tenant bounds (`SetMaxOpenConns(5)`, `SetMaxIdleConns(1)`), caches it in memory, and returns it.

#### TTL Reaper & Instant Cache Eviction:
* A background goroutine sweeps every 5 minutes, closing and evicting pools idle for over 15 minutes. This prevents connection exhaustion across replicas.
* When a tenant upgrades from Shared $\rightarrow$ Dedicated, `tenant-service` emits `tenant.infrastructure_changed`. `InfraChangedConsumer` in `order-service` calls `PoolRegistry.Evict(tenantID)`, instantly dropping the stale pool so the next request fetches the fresh Dedicated DSN.

---

## 6. Context-Propagated Transaction Management (`txctx`)

To prevent parameter pollution (`tx *sql.Tx`) across layers, we implemented Context-Propagated Executor pattern (`txctx`):

```go
type DBExecutor interface {
    ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
    QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
    QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// GetExecutor extracts *sql.Tx from context if present; otherwise returns default *sql.DB pool.
func GetExecutor(ctx context.Context, fallback DBExecutor) DBExecutor {
    if exec, ok := ctx.Value(execKey{}).(DBExecutor); ok {
        return exec
    }
    return fallback
}
```

The HTTP handler starts a transaction via `TxManager.WithTransaction(ctx, fn)`, which injects `*sql.Tx` into `ctx`. Repositories call `txctx.GetExecutor(ctx, r.dbClient)` transparently without changing interface signatures!

---

## 7. Summary of Engineering Benefits

1. **Clear Bounded Contexts**: `tenant-service` has 0 lines of domain SQL. `order-service` owns its migrations and database stack completely.
2. **Infinite Retriability**: Outbox workers and RabbitMQ consumers can crash and recover at any step without duplicate containers or orphaned schemas.
3. **Resilient Connection Scaling**: Zero persistent connections at boot, capped per-tenant pools (`MaxOpenConns: 5`), and background TTL eviction ensure PostgreSQL never runs out of file descriptors or sockets.
4. **Operational Duality**: Seamless support for both low-cost Shared Plan tenants and compliant Dedicated Enterprise tenants under a single unified codebase.
