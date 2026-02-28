# order-service (API / Domain Service - Data Plane)

`order-service` operates as the primary **Data Plane** microservice handling tenant order domain workflows, dynamic DSN connection pooling, and multi-tenant schema/database migrations.

---

## Architectural Bounds & Standards

- **Category:** Archetype A (API / Domain Service - Data Plane)
- **Layout:** `cmd/` -> `internal/{handler, service, repository, domain, infrastructure, registry, crypto}`
- **Responsibilities:** Order lifecycle management, singleflight routing resolution, dynamic multi-tenant `*sql.DB` connection pooling, and tenant DDL migrations.

For system-wide architectural rules, layer boundaries, and unit-of-work patterns, see [Clean Architecture Standards](../docs/00-clean-architecture-standards-and-layer-hierarchy.md).

---

## Service-Specific Components & Patterns

### 1. Dynamic DSN Resolution & PoolRegistry
`order-service` connects to tenant database instances dynamically based on tenant isolation mode (Shared Schema vs Dedicated Database):
- **Cache Hit:** Looks up active `*sql.DB` handle in `PoolRegistry` (`sync.RWMutex` with 15-minute TTL eviction & bounded LRU capacity).
- **Cache Miss:** Lazily fetches routing metadata (`host`, `port`, `db_name`) from `tenant-service` via internal HTTP (`/internal/tenants/:id/infrastructure/order-service` with `X-Internal-Service-Token`), derives the database password in-memory using `ORDER_SERVICE_SECRET` via HMAC-SHA256, and initializes a connection pool.
- **Cache Invalidation:** Listens for `tenant.infrastructure_changed` events broadcast over RabbitMQ topic exchange (`company.events`) on an exclusive queue to instantly purge local routing and connection pool caches across all running replicas.

### 2. Architectural Exemption: `MigrationService`
`internal/service/migration_service.go` is an intentional exception to the Layer 2 no-database-connection rule. DDL migrations for newly registered tenants must execute against an arbitrary, runtime-derived tenant DSN outside standard connection pools. It is exposed to Layer 1 via consumer-side interface `MigrationService`.

---

## Key Interfaces & APIs

### HTTP Endpoints (Port 8084)
| Method | Endpoint | Auth | Description |
| :--- | :--- | :--- | :--- |
| `POST` | `/api/orders` | Bearer `<JWT>` | Create a new order for authenticated tenant |
| `GET` | `/api/orders` | Bearer `<JWT>` | List orders for authenticated tenant |
| `GET` | `/health` | None | Health check endpoint |

### AMQP Consumer Subscriptions
| Event Key | Exchange | Action / Responsibility |
| :--- | :--- | :--- |
| `infrastructure.provisioned` | `company.events` | Triggers tenant DB DDL schema migration and emits `tenant.order_db.ready`. |
| `tenant.infrastructure_changed` | `company.events` | (Broadcast to exclusive queues) Purges local `RoutingRegistry` & `PoolRegistry` caches. |

---

## Local Development & Testing

```bash
# Run unit & repository tests
go test -v ./order-service/...

# Lint check
golangci-lint run ./order-service/...
```
