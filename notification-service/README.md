# notification-service (Event-Driven Consumer)

`notification-service` is an asynchronous event consumer responsible for handling system notification workflows, implementing barrier sync event aggregation, and dispatching welcome emails via Mailpit/SMTP.

---

## Architectural Bounds & Standards

- **Category:** Archetype B (Event-Driven Consumer)
- **Layout:** `cmd/` -> `internal/{consumer, service, domain, infrastructure, mailer}`
- **Prohibited:** Web frameworks, Gin HTTP handlers (`internal/handler`), unnecessary DB repositories.
- **Responsibilities:** AMQP consumer queue listeners, idempotent barrier sync event processing, Mailer adapter integration (SMTP/Mailpit).

For system-wide architectural rules, layer boundaries, and unit-of-work patterns, see [Clean Architecture Standards](../docs/00-clean-architecture-standards-and-layer-hierarchy.md).

---

## Service-Specific Components & Patterns

### 1. Barrier Sync Consumer Pattern
When provisioning a new workspace, welcome email dispatch requires confirmation from two independent asynchronous pipelines:
1. `workspace.ready` emitted by `tenant-service` after infrastructure provisioning & DDL migrations complete.
2. `user.created` emitted by `user-service` after identity profile creation.

`notification-service` implements a **barrier sync** using PostgreSQL inbox event tracking:
- Stores incoming events in `notification_db.inbox`.
- Evaluates whether both `workspace.ready` and `user.created` have arrived for the target `tenantID`.
- Only when both events exist within the same transaction context does it proceed to generate single-use password setup credentials.

### 2. Strict Rule: External I/O Outside Transaction
The consumer closure opens `txManager.WithTransaction` strictly for inbox deduplication, barrier checks, and writing audit logs. The external HTTP call to `auth-service` (`POST /internal/auth/setup-token`) and the SMTP email dispatch to Mailpit are executed **strictly after transaction commit** to avoid holding database connection locks during network operations.

---

## Key Interfaces & AMQP Subscriptions

### HTTP Endpoints (Port 8083)
| Method | Endpoint | Auth | Required Scope | Description |
| :--- | :--- | :--- | :--- | :--- |
| `GET` | `/api/notifications` | Bearer `<JWT>` | `notifications:read` | List notification audit records for caller's tenant |
| `GET` | `/health` | None | None | Health check endpoint |

### AMQP Consumer Subscriptions
| Event Key | Exchange | Action / Responsibility |
| :--- | :--- | :--- |
| `workspace.ready` | `company.events` | Records workspace readiness for barrier sync. |
| `user.created` | `company.events` | Records user creation for barrier sync. Triggers setup token request & welcome email when barrier is satisfied. |

### External Dependencies
* **auth-service**: `POST /internal/auth/setup-token` (Header: `X-Internal-Service-Token`) to request password setup token.
* **Mailpit SMTP**: Dispatches HTML welcome emails containing password setup links to tenant users.

---

## Local Development & Testing

```bash
# Run unit & repository tests
go test -v ./...

# Repomix packing for LLM analysis
npx repomix
```
