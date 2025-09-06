# How Do We Scale Multi-Tenancy from Shared Schema to Dedicated Databases? Hybrid Multi-Tenant Duality

*Evolving from Shared-Schema Multi-Tenancy to Dedicated Databases with Control Plane Routing, Thread-Safe Connection Registries, and Isolated Outbox Sweepers in Go*

---

## 1. The Growth Dilemma: The $100k Enterprise Requirement

When designing the multi-tenant architecture for `microservice-api`, we initially chose the **Schema-Per-Tenant** model in PostgreSQL. Every new user registration creates an isolated schema (e.g. `tenant_acme`, `tenant_globex`) inside a single PostgreSQL database instance.

This model is cost-effective, easy to manage, and provides logical data isolation.

However, as a SaaS platform matures, an Enterprise customer eventually walks in and says:
> *"We want to use your platform, but due to HIPAA/SOC2 compliance and strict data sovereignty rules, our customer data cannot share a physical database server with your other tenants. It must be hosted on an isolated, physically separate database instance."*

### The Naive Instinct: "We Need to Rewrite Everything"
The initial knee-jerk reaction might be:
- Spin up a completely separate deployment stack for the enterprise tenant.
- Duplicate microservices and application codebases.
- Maintain separate configuration pipelines.

This creates massive operational overhead and code fragmentation.

### The Engineering Solution: Hybrid Multi-Tenant Duality
Instead of building a separate platform, we extend our existing Go architecture to support **Hybrid Multi-Tenancy**. The application seamlessly handles both:
1. **Shared Tenants**: Standard tenants sharing a PostgreSQL instance via isolated schemas (`tenant_xxx`).
2. **Dedicated Tenants**: Enterprise tenants residing on physically distinct database servers.

---

## 2. Architectural Blueprint: The Control Plane Router

To support duality without polluting application code, the `public` schema in our primary database acts as the **Control Plane** (or Tenant Catalog).

We extended the `public.tenants` routing table to store location metadata:

```sql
CREATE TABLE IF NOT EXISTS public.tenants (
    id VARCHAR(255) PRIMARY KEY,
    name VARCHAR(255) NOT NULL,
    slug VARCHAR(255) NOT NULL UNIQUE,
    owner_id VARCHAR(255) NOT NULL,
    placement_type VARCHAR(50) NOT NULL DEFAULT 'SHARED', -- 'SHARED' or 'DEDICATED'
    schema_name VARCHAR(255),                            -- e.g. 'tenant_acme' (if SHARED)
    db_dsn VARCHAR(500),                                 -- Encrypted connection string (if DEDICATED)
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);
```

### Routing Logic:
- **Shared Placement (`SHARED`)**: The router points to the primary database handle, and queries target the specific schema (e.g. `SELECT * FROM tenant_acme.tenant_members`).
- **Dedicated Placement (`DEDICATED`)**: The router fetches the tenant's dedicated `db_dsn`, retrieves a dedicated connection pool, and targets the default schema inside that physically isolated database (e.g. `SELECT * FROM public.tenant_members`).

```
                               ┌───────────────────────────┐
                               │       CONTROL PLANE       │
                               │  public.tenants Metadata  │
                               └─────────────┬─────────────┘
                                             │
                      ┌──────────────────────┴──────────────────────┐
                      ▼                                             ▼
        [PLACEMENT: SHARED]                           [PLACEMENT: DEDICATED]
   ┌───────────────────────────┐                 ┌───────────────────────────┐
   │    Primary Postgres DB    │                 │   Dedicated Postgres DB   │
   │  ┌─────────────────────┐  │                 │  ┌─────────────────────┐  │
   │  │ Schema: tenant_acme │  │                 │  │ Schema: public      │  │
   │  ├─────────────────────┤  │                 │  ├─────────────────────┤  │
   │  │ Schema: tenant_glob │  │                 │  │  public.outbox      │  │
   │  ├─────────────────────┤  │                 └───────────────────────────┘
   │  │ public.outbox       │  │
   └───────────────────────────┘
```

---

## 3. The Connection Registry Pattern (`sync.RWMutex`)

Opening a new `*sql.DB` connection pool on every incoming HTTP request causes massive connection overhead, latency spikes, and eventual socket/RAM exhaustion.

To solve this, we implemented a thread-safe **Connection Registry** using Go's `sync.RWMutex`. The registry caches active database connection pools in RAM.

### How It Works:
1. **Shared Tenants**: Instantly returns the warm primary `*sql.DB` handle.
2. **Dedicated Tenants**: Checks if a pool already exists for the tenant's ID using a read lock (`RLock`). If found, returns the warm pool.
3. **Lazy Initialization**: If no pool exists, upgrades to a write lock (`Lock`), performs double-check validation, opens the connection pool with strict resource limits, pings the server, and caches it in memory.

```go
type ConnectionRegistry struct {
    mu       sync.RWMutex
    pools    map[string]*sql.DB
    sharedDB *sql.DB
}

func (r *ConnectionRegistry) GetConnection(tenant *TenantMetadata) (*sql.DB, error) {
    if tenant.PlacementType == "SHARED" {
        return r.sharedDB, nil
    }

    // Read lock check
    r.mu.RLock()
    pool, exists := r.pools[tenant.ID]
    r.mu.RUnlock()
    if exists {
        return pool, nil
    }

    // Write lock lazy initialization
    r.mu.Lock()
    defer r.mu.Unlock()

    if pool, exists := r.pools[tenant.ID]; exists {
        return pool, nil
    }

    newPool, err := sql.Open("postgres", tenant.DbDSN)
    if err != nil {
        return nil, err
    }

    // Enforce pool bounds per dedicated database to prevent starvation
    newPool.SetMaxOpenConns(15)
    newPool.SetMaxIdleConns(5)
    newPool.SetConnMaxLifetime(30 * time.Minute)

    r.pools[tenant.ID] = newPool
    return newPool, nil
}
```

---

## 4. The Outbox Pattern Duality: Solving Cross-Database Consistency

The moment you introduce physically isolated database servers, you encounter a major architectural trap with the **Transactional Outbox Pattern**.

### The Cross-Database Transaction Trap
In Challenge #4, we ensured atomic provisioning by writing business data and staging the `tenant.provisioned` event inside the exact same PostgreSQL transaction (`*sql.Tx`).

If Tenant B is on a **Dedicated Database**, you **cannot** open a single SQL transaction that writes to Tenant B's isolated server *and* the Control Plane's `public.outbox` simultaneously. They are physically separate databases on different IP addresses. Attempting 2PC (Two-Phase Commit) across distributed microservices is slow, brittle, and introduces complex failure modes.

### The Solution: Isolated Outbox Per Database
Every dedicated database carries its *own* `public.outbox` table.

1. **Transactional Write**: `tenant-service` opens a transaction on Tenant B's dedicated database. It seeds `public.tenant_members` and writes the event to `dedicated_db.public.outbox` in the **same local transaction**. Local ACID guarantees remain 100% intact.
2. **Multi-Pool Outbox Sweeper**: The Go `OutboxWorker` is upgraded. Instead of polling only the primary database, it retrieves all active connection pools from `ConnectionRegistry` and executes `FOR UPDATE SKIP LOCKED` across all databases.

```
┌─────────────────────────────────────────────────────────────────────────────┐
## 5. Production Implementation in Go

### 1. Connection Registry Infrastructure
Inspect [tenant_registry.go](../tenant-service/internal/infrastructure/postgres/tenant_registry.go):
- Thread-safe pool caching with `sync.RWMutex`.
- Configured connection limits (`SetMaxOpenConns(15)`).
- Clean resource teardown on service shutdown (`CloseAll()`).

### 2. Duality Provisioning Logic in `tenant_service.go`
Inspect [tenant_service.go](../tenant-service/internal/service/tenant_service.go#L50-L115):
- Automatically branches logic based on `PlacementType`:
  - `SHARED`: Executes `CREATE SCHEMA tenant_xxx`, runs migrations, seeds owner, stages outbox event in primary DB.
  - `DEDICATED`: Connects to `DbDSN` via `ConnectionRegistry`, runs migrations on target `public` schema, seeds owner, stages outbox event in dedicated DB outbox.

### 3. Multi-Pool Outbox Worker Sweeper in `outbox_worker.go`
Inspect [outbox_worker.go](../tenant-service/internal/worker/outbox_worker.go#L95-L135):
- Sweeps primary DB outbox as well as all active dedicated pools registered in `ConnectionRegistry`:
```go
func (w *OutboxWorker) processBatch(ctx context.Context) {
    // Sweep primary database outbox
    w.processBatchOnDB(ctx, nil)

    // Sweep all active dedicated tenant database outboxes
    if w.registry != nil {
        for _, pool := range w.registry.GetAllActiveDedicatedPools() {
            w.processBatchOnDB(ctx, pool)
        }
    }
}
```

### 4. Local Simulation via Docker Compose
Inspect [docker-compose.yml](../broker/docker-compose.yml):
- Primary Database (`postgres`): Port `5432` hosting Control Plane and shared tenant schemas.
- Dedicated Database (`postgres-dedicated`): Port `5433` hosting `broker_dedicated_db` initialized via [init-dedicated.sql](../broker/init-dedicated.sql).

---

## 6. Architecture Comparison Matrix

| Architectural Feature | Shared Schema (Schema-per-Tenant) | Dedicated Database | Hybrid Duality (Our Architecture) |
| :--- | :--- | :--- | :--- |
| **Physical Isolation** | Logical (Schema boundary) | Physical (Separate DB instance) | Dynamic per tenant tier |
| **Infrastructure Cost** | Low (Shared CPU/RAM/Disk) | Higher (Dedicated compute/RAM) | Optimal (Cheap shared + High-tier dedicated) |
| **Connection Overhead** | Single DB connection pool | Scaled per dedicated tenant pool | Cached & bounded via `ConnectionRegistry` |
| **Outbox Transactionality** | Single DB `public.outbox` | Per-instance `public.outbox` | Multi-pool worker sweeping all active outboxes |
| **Code Maintenance** | Unified repository patterns | Unified repository patterns | **Zero code duplication**; handled via DI & Registry |

---

## Conclusion

By introducing a **Control Plane Tenant Catalog**, a **Thread-Safe Connection Registry**, and a **Multi-Pool Outbox Sweeper**, we transformed `microservice-api` from a single-database system into an Enterprise-ready **Hybrid Multi-Tenant Platform** without breaking existing code or rewriting services.
