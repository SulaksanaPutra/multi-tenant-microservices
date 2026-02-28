# tenant-service (API / Domain Service - Control Plane)

`tenant-service` acts as the **Control Plane** authority responsible for multi-tenant workspace lifecycle management, tenant metadata registration, transactional outbox publishing, and dynamic database routing activation.

---

## Architectural Bounds & Standards

- **Category:** Archetype A (API / Domain Service - Control Plane)
- **Layout:** `cmd/` -> `internal/{handler, service, repository, domain, worker, middleware}`
- **Responsibilities:** Tenant registration API, control-plane metadata storage (`tenant_services` schema), Transactional Outbox pattern (`workspace.initiated`), dynamic routing metadata activation, and broadcast cache invalidation.

For system-wide architectural rules, layer boundaries, and unit-of-work patterns, see [Clean Architecture Standards](../docs/00-clean-architecture-standards-and-layer-hierarchy.md).

---

## Service-Specific Components & Patterns

### 1. Password-less Registration & Transactional Outbox
During `POST /api/register`, `tenant-service` writes the tenant metadata (status `PENDING`) and saves a `workspace.initiated` event frame to the `outbox` table in **a single database transaction**. The background `outboxWorker` polls pending outbox records (`SELECT FOR UPDATE SKIP LOCKED`) and publishes events to RabbitMQ without risking dual-write inconsistencies or leaking passwords across event streams.

### 2. Infrastructure Routing Metadata & Broadcast Invalidation
- **Internal Infrastructure Endpoint:** Exposes `GET /internal/tenants/:id/infrastructure/:serviceName` (protected by `X-Internal-Service-Token`) so domain services like `order-service` can lazily fetch database connection coordinates (`host`, `port`, `db_name`).
- **Broadcast Cache Invalidation:** When a tenant's database infrastructure changes (e.g. failover, IP/port rebind, plan upgrade/downgrade), `tenant-service` emits `tenant.infrastructure_changed` over a RabbitMQ topic exchange to force downstream replicas to instantly purge local connection pool caches.

---

## Key Interfaces & APIs

### HTTP Endpoints (Port 8082)
| Method | Endpoint | Auth | Description |
| :--- | :--- | :--- | :--- |
| `POST` | `/api/register` | None | Register a new tenant workspace & initiate async provisioning |
| `GET` | `/internal/tenants/:id/infrastructure/:serviceName` | `X-Internal-Service-Token` | Internal endpoint for domain service DSN resolution |
| `GET` | `/health` | None | Health check endpoint |

### AMQP Published Events & Subscriptions
| Event Key | Role | Purpose |
| :--- | :--- | :--- |
| `workspace.initiated` | Published | Triggers container provisioning (`infra-provisioner`) and user creation (`user-service`). |
| `tenant.order_db.ready` | Subscribed | Confirms tenant DB DDL migrations complete; activates workspace (`ACTIVE`) & emits `workspace.ready`. |
| `workspace.ready` | Published | Signals workspace activation complete for barrier sync listeners. |
| `tenant.infrastructure_changed` | Published | Fanout broadcast key to clear downstream DSN connection pool caches. |

---

## Local Development & Testing

```bash
# Run unit & repository tests
go test -v ./tenant-service/...

# Lint check
golangci-lint run ./tenant-service/...
```
