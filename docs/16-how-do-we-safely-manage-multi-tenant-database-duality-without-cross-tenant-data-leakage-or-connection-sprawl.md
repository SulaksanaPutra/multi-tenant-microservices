# Managing Multi-Tenant Database Duality: Connection Pooling and Isolation

*Architectural notes on constructor dependency arity, dynamic tenant DSN resolution, and bounded connection pooling in Go.*

---

## 1. Problem Context: Multi-Tenant Duality in Data Planes

Different microservices operate under different data isolation requirements:

1. **Control-Plane Services (`user-service`, `tenant-service`)**:
   - Operate against a single, static database (`user_db`, `tenant_manager_db`).
   - Repositories are initialized once at startup and reused throughout the process lifetime.
2. **Data-Plane Services (`order-service`)**:
   - Execute business transactions across a **Hybrid Multi-Tenant Duality**:
     - *Shared Model*: Multiple tenants share a PostgreSQL instance, partitioned by schema (`tenant_<id>_order_db`).
     - *Dedicated Model*: Enterprise tenants have dedicated PostgreSQL containers on isolated IPs/ports.

```text
[ Client App ]
      │  HTTP Requests (Header: tenant-x-id)
      ▼
[ order-service Router ]
      │
      ▼ (tenantDBResolver Middleware)
[ PoolRegistry & RoutingRegistry ]
      │
      ├─────────────────────────────────────────┐
      ▼ (Shared Plan)                           ▼ (Dedicated Plan)
  Fetch shared_db pool                    Derive HMAC password & resolve
  Schema: tenant_123_order_db             dedicated container pool
      │                                   Schema: public
      └────────────────────┬────────────────────┘
                           ▼
             [ tenantdb.Config Instantiation ]
                           ▼
             [ OrderRepository (Request-Scoped) ]
                           ▼
             [ Execute Query on Target Tenant DB ]
```

---

## 2. Constructor Design: Params Structs vs. Positional Arguments

In our codebase, repositories use simple positional arguments (`NewOrderRepository(db, schema)`), whereas infrastructure consumers and resolvers use parameter structs (`tenantdb.Params`, `WorkspaceInitiatedConsumerParams`).

This is an intentional design choice based on **Dependency Arity**:

- **Low Arity (1 to 2 dependencies)**: A repository typically takes a database executor and an optional schema string. Positional arguments are clear, explicit, and compile-time verified.
- **High Arity (4+ dependencies)**: Infrastructure components aggregate message brokers, transaction managers, inbox stores, configuration secrets, and domain services. Passing 6+ positional parameters creates messy call sites and makes it easy to accidentally swap parameters of the same type (like multiple string configurations). Grouping them into a `Params` struct makes call sites readable and simplifies adding optional parameters later.

---

## 3. Preventing Cross-Tenant Data Leakage

To prevent queries from leaking across tenants:

1. **Request-Scoped Configuration Injection**:
   Middleware resolves the tenant's database connection and schema name, packing them into an immutable `tenantdb.Config` struct stored in the request context:
   ```go
   type Config struct {
       db         *sql.DB
       schemaName string
   }
   ```
2. **Dynamic Schema Qualification**:
   Repositories extract `tenantdb.Config` from the context and prepend the schema name to query identifiers:
   ```go
   query := fmt.Sprintf("SELECT id, total FROM %s.orders WHERE customer_id = $1", pq.QuoteIdentifier(cfg.SchemaName))
   ```
3. **Dedicated Connection Pool Limits**:
   Dynamic pools are capped with tight upper bounds (`SetMaxOpenConns(5)`, `SetMaxIdleConns(2)`) and an idle timeout. An hourly sweeper cleans up inactive pools to prevent connection leaks on long-running worker processes.

---

## 4. Architectural Invariants & Operational Trade-offs

- **Request-Scoped Tenant Binding**: Repositories never maintain static or global tenant state; tenant DB connections and schemas are injected per-request via immutable `tenantdb.Config`.
- **Constructor Arity Rule**: Low arity (1-2 dependencies) uses explicit positional arguments; high arity (4+ dependencies) uses dedicated `Params` structs to prevent accidental argument swapping.
- **Bounded Dynamic Pools**: Dynamic tenant connection pools enforce strict upper bounds (`SetMaxOpenConns`, `SetMaxIdleConns`) and hourly idle sweeping to prevent database connection exhaustion under multi-tenant scale.
