# tenant-service (API / Domain Service - Control Plane)

`tenant-service` acts as the **Control Plane** authority responsible for multi-tenant workspace lifecycle management, Zero-Trust tenant profile management, tenant metadata registration, transactional outbox publishing, and dynamic database routing activation.

---

## Architectural Bounds & Standards

- **Category:** Archetype A (API / Domain Service - Control Plane)
- **Layout:** `cmd/` -> `internal/{handler, service, repository, domain, worker, middleware}`
- **Responsibilities:** Tenant registration API, Zero-Trust `/api/tenants/me` control plane APIs, metadata storage (`tenant_services` schema), Transactional Outbox pattern (`workspace.initiated`), dynamic routing metadata activation, and broadcast cache invalidation (`tenant.infrastructure_changed`).

For system-wide architectural rules, layer boundaries, and unit-of-work patterns, see [Clean Architecture Standards](../docs/00-clean-architecture-standards-and-layer-hierarchy.md).

---

## Service-Specific Components & Patterns

### 1. Password-less Registration & Transactional Outbox
During `POST /api/tenants/register`, `tenant-service` writes tenant metadata (status `PENDING`) and saves a `workspace.initiated` event frame to the `outbox` table in **a single database transaction**. The background `outboxWorker` polls pending outbox records (`SELECT FOR UPDATE SKIP LOCKED`) and publishes events to RabbitMQ without risking dual-write inconsistencies or leaking passwords across event streams.

### 2. Zero-Trust `/api/tenants/me` Context Derivation
Client requests do not pass `tenant_id` query parameters. Instead, `tenant-service` derives `tenantID` strictly from authenticated RS256 JWT token claims (`c.GetString("tenantID")`) to enforce Zero-Trust session isolation and prevent IDOR attacks.

### 3. Infrastructure Routing Metadata & Broadcast Invalidation
- **Internal Infrastructure Endpoint:** Exposes `GET /internal/tenants/:id/infrastructure/:serviceName` (protected by `X-Internal-Service-Token`) so domain services like `order-service` can lazily fetch database connection coordinates (`host`, `port`, `db_name`).
- **Broadcast Cache Invalidation:** When a tenant's database infrastructure changes (e.g. failover, IP/port rebind, plan upgrade/downgrade), `tenant-service` emits `tenant.infrastructure_changed` over a RabbitMQ topic exchange to force downstream replicas to instantly purge local connection pool caches.

---

## Key Interfaces & APIs

### HTTP Endpoints (Port 8082)
| Method | Endpoint | Auth | Required Scope | Description |
| :--- | :--- | :--- | :--- | :--- |
| `POST` | `/api/tenants/register` | None | Public | Register a new tenant workspace & initiate async provisioning |
| `GET` | `/api/tenants/me` | Bearer `<JWT>` | `tenants:read` | Retrieve authenticated tenant workspace profile metadata |
| `PUT` | `/api/tenants/me` | Bearer `<JWT>` | `tenants:write` | Update tenant metadata (name, slug, owner info) |
| `PUT` | `/api/tenants/me/plan` | Bearer `<JWT>` | `tenants:write` | Upgrade/downgrade tenant isolation plan (`shared` / `dedicated`) |
| `GET` | `/internal/tenants/:id/infrastructure/:serviceName` | `X-Internal-Service-Token` | Internal | Internal endpoint for domain service DSN resolution |
| `GET` | `/health` | None | None | Health check endpoint |

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
go test -v ./...

# Repomix packing for LLM analysis
npx repomix
```
