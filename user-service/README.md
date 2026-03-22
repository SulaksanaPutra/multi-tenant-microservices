# user-service (API / Domain Service - User Identity)

`user-service` is responsible for user profile management, identity lifecycle tracking, workspace registration identity bootstrapping, and role management delegation to `auth-service`.

---

## Architectural Bounds & Standards

- **Category:** Archetype A (API / Domain Service - User Identity)
- **Layout:** `cmd/` -> `internal/{handler, service, repository, domain, infrastructure, consumer}`
- **Responsibilities:** User profile management, identity lifecycle tracking, `workspace.initiated` event handling, emitting `user.created` events, and delegating role/permission management to `auth-service` via Layer 2 Clean Architecture ports.

For system-wide architectural rules, layer boundaries, and unit-of-work patterns, see [Clean Architecture Standards](../docs/00-clean-architecture-standards-and-layer-hierarchy.md).

---

## Service-Specific Components & Patterns

### 1. Workspace Registration Identity Bootstrap
When `tenant-service` emits `workspace.initiated`, `user-service` consumes the message, creates the initial admin user profile record in `user_db`, and publishes `user.created` to `company.events`. This satisfies one arm of the **barrier sync** in `notification-service`.

### 2. Role & Permission Management Delegation (Clean Architecture)
`user-service` acts as the domain entry point for role assignments and role creations (`/api/users/roles`, `/api/users/:user_id/role`).
- **Layer 1 (`UserHandler`):** Strictly depends on `UserServiceInterface` (no raw HTTP client handles in controllers).
- **Layer 2 (`UserService`):** Declares consumer-side port interface `RoleClient` (`internal/service/user_service.go`) and orchestrates domain logic.
- **Layer 3 (`AuthClient`):** Implements `RoleClient` via outbound HTTP requests to `auth-service:8085` (`internal/infrastructure/authclient/auth_client.go`).

---

## Key Interfaces & APIs

### HTTP Endpoints (Port 8081)
| Method | Endpoint | Auth | Required Scope | Description |
| :--- | :--- | :--- | :--- | :--- |
| `GET` | `/api/users` | Bearer `<JWT>` | `users:read` | List users belonging to caller's tenant |
| `GET` | `/api/users/me` | Bearer `<JWT>` | `users:read` | Get authenticated user profile details |
| `PUT` | `/api/users/me` | Bearer `<JWT>` | `users:write` | Update authenticated user profile details |
| `GET` | `/api/users/permissions` | Bearer `<JWT>` | `users:roles:manage` | List available system permissions catalog |
| `POST` | `/api/users/roles` | Bearer `<JWT>` | `users:roles:manage` | Create tenant-scoped custom role |
| `GET` | `/api/users/roles` | Bearer `<JWT>` | `users:roles:manage` | List available roles for tenant |
| `GET` | `/api/users/:user_id/role` | Bearer `<JWT>` | `users:read` | Retrieve role assignment for target user |
| `PUT` | `/api/users/:user_id/role` | Bearer `<JWT>` | `users:roles:manage` | Assign role to target user |
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
