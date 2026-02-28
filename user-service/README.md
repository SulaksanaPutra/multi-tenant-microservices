# user-service (API / Domain Service - User Identity)

`user-service` is responsible for user profile management, identity lifecycle tracking, and asynchronous event consumption during workspace provisioning.

---

## Architectural Bounds & Standards

- **Category:** Archetype A (API / Domain Service - User Identity)
- **Layout:** `cmd/` -> `internal/{handler, service, repository, domain, consumer}`
- **Responsibilities:** User profile management, identity lifecycle tracking, `workspace.initiated` event handling, and emitting `user.created` events.

For system-wide architectural rules, layer boundaries, and unit-of-work patterns, see [Clean Architecture Standards](../docs/00-clean-architecture-standards-and-layer-hierarchy.md).

---

## Service-Specific Components & Patterns

### Workspace Registration Identity Bootstrap
When `tenant-service` emits `workspace.initiated`, `user-service` consumes the message, creates the initial admin user profile record in `user_db`, and publishes `user.created` to `company.events`. This satisfies one arm of the **barrier sync** in `notification-service`.

---

## Key Interfaces & APIs

### HTTP Endpoints (Port 8081)
| Method | Endpoint | Auth | Description |
| :--- | :--- | :--- | :--- |
| `GET` | `/api/users/me` | Bearer `<JWT>` | Get authenticated user profile details |
| `GET` | `/health` | None | Health check endpoint |

### AMQP Published Events & Subscriptions
| Event Key | Role | Purpose |
| :--- | :--- | :--- |
| `workspace.initiated` | Subscribed | Triggers initial user profile creation. |
| `user.created` | Published | Signals user profile creation to `notification-service` for barrier sync. |

---

## Local Development & Testing

```bash
# Run unit & repository tests
go test -v ./user-service/...

# Lint check
golangci-lint run ./user-service/...
```
