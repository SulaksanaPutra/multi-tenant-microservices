# How Do We Safely Manage Multi-Tenant Database Duality Without Cross-Tenant Data Leakage or Connection Sprawl?

*Engineering Notes on Constructor Arity, Dynamic DSN Resolution, Bounded LRU Connection Pooling, and Clean Architecture Isolation in Go*

---

## 1. Problem Context & Architectural Duality

In a multi-tenant microservices platform, different services operate under fundamentally different data isolation models:

1. **Control-Plane Services (`user-service`, `tenant-service`)**: These services manage global metadata and single-tenant identity records. They operate against a single, static control-plane database (`userDB`, `tenantManagerDB`). Their repositories are instantiated once at startup and reused for the entire application lifecycle.
2. **Data-Plane Services (`order-service`)**: This service executes business transactions across a **Hybrid Multi-Tenant Duality**:
   * **Shared Isolation Model**: Multiple tenants share a single PostgreSQL database instance (`shared_db`), segregated into isolated tenant schemas (`tenant_<id>_order_db`).
   * **Dedicated Isolation Model**: High-tier enterprise tenants are provisioned with dedicated PostgreSQL Docker containers (`172.20.0.X:5432`) running on separate ports/IPs with dynamic HMAC-derived credentials.

```text
+-----------------------------------------------------------------------------------+
|                     Multi-Tenant Database Duality Architecture                    |
+-----------------------------------------------------------------------------------+

[ Client App ]
      │  HTTP Requests (Header: tenant-x-id)
      ▼
[ order-service API Gateway / Router ]
      │
      ▼ (tenantDBResolver Middleware)
[ PoolRegistry & RoutingRegistry ]
      │
      ├─────────────────────────────────────────┐
      ▼ (Shared Plan)                           ▼ (Dedicated Plan)
  Fetch shared_db pool                    Derive HMAC password & fetch/open
  Schema: tenant_123_order_db             dedicated container pool (172.20.0.5)
      │                                   Schema: public
      └────────────────────┬────────────────────┘
                           ▼
             [ tenantdb.Config Instantiation ]
                           ▼
             [ OrderRepository (Request-Scoped) ]
                           ▼
             [ Execute Query on Target Tenant DB ]
```

This duality introduces two major architectural questions:
1. *Why do constructor parameter patterns (`Params` structs vs. positional arguments) differ between the Consumer/Resolver layers and the Service/Repository layers?*
2. *How does `order-service` safely route queries to dynamic tenant databases without risk of cross-tenant data leakage or TCP connection sprawl?*

---

## 2. Constructor Patterns: `Params` Structs vs. Positional Arguments

A common question during codebase audits is why consumer and resolver constructors use a `Params` struct (`WorkspaceInitiatedConsumerParams`, `tenantdb.Params`), while repository constructors use simple positional arguments (`NewUserRepository(dbClient)`).

This distinction is driven by three Go design principles: **Dependency Arity**, **Thread-Safe Encapsulation**, and **Refactoring Blast Radius**.

### 2.1 Dependency Arity (1–2 Args vs. 4–7 Args)

Go constructors should maintain low argument counts to preserve readability and type safety:

* **Repositories & Domain Services (Low Arity)**: A repository typically depends on 1 database client (`func NewUserRepository(db *postgres.Client)`). A domain service depends on 1 repository and 1 outbox repo (`func NewUserService(userRepo, outboxRepo)`). Positional arguments are unambiguous and type-checked by the compiler.
* **Consumers & Resolvers (High Arity)**: Infrastructure entrypoints aggregate messaging drivers (`*rabbitmq.Client`), transaction managers (`TxManager`), idempotency repositories (`InboxRepository`), domain services (`UserService`), and infrastructure credentials (`SharedSecret`, `SharedDBPass`). Passing 6+ positional parameters introduces **argument sprawl** and risks swapping interface implementations.

### 2.2 Thread-Safe Encapsulation (Exported DTO vs. Unexported State)

Consumers and background workers launch goroutines (`go c.runConsumerLoop()`). Their internal runtime fields MUST be unexported to prevent concurrent mutation:

```go
// Exported DTO for initialization (Ephemeral parameter container)
type WorkspaceInitiatedConsumerParams struct {
    TxManager       TxManager
    Client          *rabbitmq.Client
    InboxRepository InboxRepository
    UserService     UserService
}

// Unexported internal fields (Thread-safe long-lived runtime state)
type WorkspaceInitiatedConsumer struct {
    txManager       TxManager
    client          *rabbitmq.Client
    inboxRepository InboxRepository
    userService     UserService
}

func NewWorkspaceInitiatedConsumer(params WorkspaceInitiatedConsumerParams) (*WorkspaceInitiatedConsumer, error) {
    consumer := &WorkspaceInitiatedConsumer{
        txManager:       params.TxManager,
        client:          params.Client,
        inboxRepository: params.InboxRepository,
        userService:     params.UserService,
    }
    if err := consumer.setupTopology(); err != nil {
        return nil, err
    }
    return consumer, nil
}
```

By separating `Params` (exported struct) from `Consumer` (unexported fields), callers in `main.go` can initialize fields by name without exposing the active worker's internal state to runtime corruption.

---

## 3. Zero-Leakage Dynamic Multi-Tenant Routing Engine

To handle different databases per tenant without hardcoding static connections, `order-service` uses an **Outer-Layer `tenantDBResolver` Middleware** and a **Request-Scoped `OrderRepository`**.

### 3.1 Request-Scoped DSN Resolution

When an HTTP request arrives at `order-service` with a `tenant-x-id` header:

1. **`tenantDBResolver` Middleware**: Intercepts the request and checks the in-memory `RoutingRegistry` materialized view.
2. **Deterministic Credential Derivation**: For dedicated tenants, the resolver derives the PostgreSQL password in memory using HMAC-SHA256 (`crypto.DeriveTenantDBPassword(secret, tenantID)`), avoiding secret transmission over AMQP.
3. **Pool Acquisition**: Fetches or opens the connection pool (`*sql.DB`) from `PoolRegistry`.
4. **Context Injection**: Wraps the target pool and schema name into a `tenantdb.Config` struct:

```go
// order-service/internal/repository/order_repository.go
type OrderRepository struct {
    cfg tenantdb.Config
}

func NewOrderRepository(cfg tenantdb.Config) *OrderRepository {
    return &OrderRepository{cfg: cfg}
}
```

### 3.2 Schema-Level SQL Isolation

`OrderRepository` does not assume a global schema. Every query dynamically formats its target table using `pq.QuoteIdentifier`:

```go
func (r *OrderRepository) CreateOrder(ctx context.Context, order domain.Order) error {
    if r.cfg.DB == nil {
        return errors.New("order repository: database handle is nil")
    }

    schemaName := r.cfg.SchemaName
    if schemaName == "" {
        schemaName = "public"
    }

    exec := txcontext.GetExecutor(ctx, r.cfg.DB)

    query := fmt.Sprintf(`
        INSERT INTO %s.orders (id, tenant_id, customer_id, status, amount, created_at, updated_at)
        VALUES ($1, $2, $3, $4, $5, NOW(), NOW())
    `, pq.QuoteIdentifier(schemaName))

    _, err := exec.ExecContext(ctx, query, order.ID, order.TenantID, order.CustomerID, order.Status, order.Amount)
    return err
}
```

Because `pq.QuoteIdentifier` escapes special characters, SQL injection attacks via schema names are completely prevented.

---

## 4. Preventing Connection Sprawl via Bounded LRU & Async Reaper

If `order-service` maintains persistent connection pools for thousands of dedicated tenant databases, host memory and file descriptors will quickly exhaust (`EMFILE`).

To prevent connection sprawl, `PoolRegistry` enforces a **Bounded LRU Cache with an Asynchronous Background Reaper**:

```go
// StartReaper runs a 3-minute ticker to evict idle tenant pools
func (r *PoolRegistry) StartReaper(ctx context.Context) {
    go func() {
        ticker := time.NewTicker(3 * time.Minute)
        defer ticker.Stop()

        for {
            select {
            case <-ctx.Done():
                return
            case <-ticker.C:
                r.reapIdlePools(15 * time.Minute)
            }
        }
    }()
}
```

### Resource Management Policies:
1. **LRU Capacity Limit**: If `PoolRegistry` reaches maximum capacity (e.g. 100 active pools), the Least-Recently-Used tenant pool is evicted.
2. **Idle TTL Eviction**: Pools idle for over 15 minutes are evicted by the reaper.
3. **Graceful Socket Reclamation**: When a pool is evicted, `db.Close()` is called asynchronously via a cleanup queue to drain active connections without blocking the main worker thread.

---

## 5. Architectural Summary Table

| Layer / Component | Dependency Arity | Constructor Design | Primary Responsibility |
| :--- | :---: | :--- | :--- |
| **Repository Layer** | 1 | Positional (`NewUserRepository(db)`) | Pure SQL execution against injected DB handle |
| **Service Layer** | 1 – 2 | Positional (`NewUserService(repo, outbox)`) | Domain business logic and transaction boundaries |
| **Consumer Layer** | 4 – 7 | `Params` Struct (`NewConsumer(Params)`) | AMQP topology setup, unit-of-work, poison-pill mitigation |
| **TenantDB Resolver** | 6 | `Params` Struct (`NewResolver(Params)`) | Dynamic HMAC secret derivation & DSN resolution |
| **Pool Registry** | 0 | Positional (`NewPoolRegistry()`) | Bounded LRU caching & connection pool lifecycle management |
