# Scaling Multi-Tenancy: Hybrid Duality with Shared Schemas and Dedicated Databases

*Combining logical schema isolation with physically isolated database containers using a centralized control plane router.*

---

## 1. The Multi-Tenant Architecture Trade-off

When building multi-tenant SaaS backends, database isolation typically follows one of two models:

1. **Shared Schema / Schema-per-Tenant**:
   - Multiple tenants share a single PostgreSQL cluster. Each tenant is assigned a dedicated schema (e.g. `tenant_acme`, `tenant_globex`).
   - Cost-effective and straightforward to operate. Connection pools are shared, and hardware utilization is high.
   - Trade-off: No physical compute/storage isolation; potential noisy neighbor issues and regulatory limitations (e.g., SOC2 or HIPAA compliance requiring distinct physical infrastructure).

2. **Dedicated Database per Tenant**:
   - Each tenant receives an isolated database instance or dedicated container.
   - Guarantees strict resource isolation, independent backup/restore cycles, and customer-specific encryption keys.
   - Trade-off: Higher infrastructure cost, dynamic connection management overhead, and increased operational complexity.

Instead of forcing a single model across all tiers, our platform implements **Hybrid Multi-Tenant Duality**: standard workspaces use shared schemas by default, while enterprise tiers are provisioned with dedicated database containers.

---

## 2. Control Plane Routing Architecture

The `public` schema in our primary database acts as the **Control Plane** (or Tenant Catalog), tracking workspace metadata and storage routing:

```sql
CREATE TABLE IF NOT EXISTS public.tenants (
    id VARCHAR(255) PRIMARY KEY,
    name VARCHAR(255) NOT NULL,
    slug VARCHAR(255) NOT NULL UNIQUE,
    owner_id VARCHAR(255) NOT NULL,
    placement_type VARCHAR(50) NOT NULL DEFAULT 'SHARED', -- 'SHARED' or 'DEDICATED'
    schema_name VARCHAR(255),                            -- e.g. 'tenant_acme' (if SHARED)
    db_dsn VARCHAR(500),                                 -- Connection identifier or DSN (if DEDICATED)
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);
```

```text
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
   │  │ Schema: tenant_glob │  │                 │  │ public.outbox       │  │
   │  ├─────────────────────┤  │                 └───────────────────────────┘
   │  │ public.outbox       │  │
   └───────────────────────────┘
```

### Routing Resolution
- **Shared Placement (`SHARED`)**: The application connects to the primary shared database pool, and queries specify the tenant schema name (e.g. `tenant_acme.orders`).
- **Dedicated Placement (`DEDICATED`)**: The application resolves the dedicated DSN, retrieves or initializes a connection pool for that specific database, and queries the default schema (`public.orders`).

---

## 3. Dynamic Connection Pooling

In a hybrid setup, domain services cannot rely solely on a single static `*sql.DB` pool initialized at startup. They require a dynamic connection manager that caches and manages pools per dedicated tenant:

```go
type MultiTenantDBResolver struct {
    sharedDB     *sql.DB
    dedicatedMu  sync.RWMutex
    dedicatedDBs map[string]*sql.DB // keyed by tenantID
}

func (r *MultiTenantDBResolver) Resolve(ctx context.Context, tenantID string, routing RoutingInfo) (*sql.DB, string, error) {
    if routing.PlacementType == "SHARED" {
        return r.sharedDB, routing.SchemaName, nil
    }

    // Dedicated tenant: check read lock first
    r.dedicatedMu.RLock()
    db, exists := r.dedicatedDBs[tenantID]
    r.dedicatedMu.RUnlock()
    if exists {
        return db, "public", nil
    }

    // Acquire write lock to initialize connection pool
    r.dedicatedMu.Lock()
    defer r.dedicatedMu.Unlock()

    if db, exists := r.dedicatedDBs[tenantID]; exists {
        return db, "public", nil
    }

    newDB, err := sql.Open("postgres", routing.DedicatedDSN)
    if err != nil {
        return nil, "", fmt.Errorf("failed to open dedicated tenant pool: %w", err)
    }
    newDB.SetMaxOpenConns(10)
    newDB.SetMaxIdleConns(2)
    newDB.SetConnMaxLifetime(10 * time.Minute)

    r.dedicatedDBs[tenantID] = newDB
    return newDB, "public", nil
}
```

---

## 4. Outbox Worker Coordination Across Topologies

With hybrid multi-tenancy, outbox events can reside in different databases depending on tenant placement:

- **Shared Tenants**: Outbox records live in `public.outbox` on the primary database cluster.
- **Dedicated Tenants**: Outbox records live in `public.outbox` inside each dedicated container.

To prevent missing events, background outbox workers poll across active dedicated pools or subscribe to in-memory notification channels triggered during local writes. Storing the outbox table locally within each dedicated container ensures the outbox write remains atomic with the business transaction, preserving transactional integrity regardless of where the tenant is hosted.

---

## 5. Architectural Invariants & Operational Trade-offs

- **Topology Transparency**: Domain services execute queries through resolved `*sql.DB` executors, remaining unaware of physical infrastructure placement.
- **Connection Boundedness**: Dynamic connection pools enforce conservative maximum connection limits and idle timeouts to prevent connection sprawl.
- **Outbox Locality**: Outbox records are co-located in the same database instance as the tenant's domain data, preserving atomicity during writes.
