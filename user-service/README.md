# user-service (API / Domain Service - User Identity)

`user-service` is responsible for user profile management, identity lifecycle tracking, and workspace registration identity bootstrapping. RBAC (roles, permissions, role assignments) is owned exclusively by `auth-service`; user-service never proxies role-management calls.

---

## Architectural Bounds & Standards

- **Category:** Archetype A (API / Domain Service - User Identity)
- **Layout:** `cmd/` -> `internal/{handler, service, repository, domain, infrastructure, consumer}`
- **Responsibilities:** User profile management, identity lifecycle tracking, `workspace.initiated` event handling, and emitting `user.created` events. Role/permission management is the sole responsibility of `auth-service`.

For system-wide architectural rules, layer boundaries, and unit-of-work patterns, see [Clean Architecture Standards](../docs/00-clean-architecture-standards-and-layer-hierarchy.md).

---

## Service-Specific Components & Patterns

### 1. Workspace Registration Identity Bootstrap
When `tenant-service` emits `workspace.initiated`, `user-service` consumes the message, creates the initial admin user profile record in `user_db`, and publishes `user.created` to `company.events`. This satisfies one arm of the **barrier sync** in `notification-service`.

---

## Key Interfaces & APIs

### HTTP Endpoints (Port 8081)
| Method | Endpoint | Auth | Required Scope | Description |
| :--- | :--- | :--- | :--- | :--- |
| `GET` | `/api/users` | Bearer `<JWT>` | `users:read` | List users belonging to caller's tenant |
| `GET` | `/api/users/me` | Bearer `<JWT>` | `users:read` | Get authenticated user profile details |
| `PUT` | `/api/users/me` | Bearer `<JWT>` | `users:write` | Update authenticated user profile details |
| `GET` | `/health` | None | None | Health check endpoint |

### AMQP Published Events & Subscriptions
| Event Key | Role | Purpose |
| :--- | :--- | :--- |
| `workspace.initiated` | Subscribed | Triggers initial user profile creation. |
| `user.created` | Published | Signals user profile creation to `notification-service` for barrier sync. |

---

## Local Development & Testing

```bash
# Run unit & repository tests
go test -v ./...

# Repomix packing for LLM analysis
npx repomix
```
