# infra-provisioner (Isolated Infrastructure Provisioner)

`infra-provisioner` is an isolated background worker service responsible for dynamic Docker container provisioning, PostgreSQL database bootstrapping, resource limit enforcement, and credential protection.

---

## Architectural Bounds & Standards

- **Category:** Archetype C (Isolated Infrastructure Provisioner)
- **Layout:** `cmd/` -> `internal/{worker, provisioner, infrastructure, crypto, domain}`
- **Prohibited:** Web frameworks, Gin, tenant web middleware, HTTP transport abstractions.
- **Responsibilities:** Docker SDK integration, QoS=1 AMQP queue prefetch, declarative database bootstrapping, resource-restricted container orchestration.

For system-wide architectural rules, layer boundaries, and unit-of-work patterns, see [Clean Architecture Standards](../docs/00-clean-architecture-standards-and-layer-hierarchy.md).

---

## Service-Specific Components & Patterns

### 1. Isolated Docker Container Orchestration
`infra-provisioner` consumes `workspace.initiated` events over RabbitMQ:
- **Shared Plan:** Emits `infrastructure.provisioned` immediately with default shared database coordinates.
- **Dedicated Plan:** Spins up dedicated PostgreSQL containers via Docker SDK:
  - Enforces container resource limits: **512MB RAM**, **0.5 CPU cores**.
  - Declaratively bootstraps tenant databases, roles, and schema privileges using root credentials held strictly inside the provisioner.
  - Polls container health via `pg_isready` before emitting completion events.
  - **Deterministic Secret Derivation:** Derives service database credentials in-memory using HMAC-SHA256 without sending raw database passwords over AMQP event payloads.

### 2. Prefetch & Task Concurrency Control
Configured with AMQP `QoS prefetch count = 1` to ensure sequential, resource-aware container provisioning and prevent Docker socket starvation during high-concurrency registration spikes.

---

## Key AMQP Event Interface

### AMQP Subscriptions & Events
| Event Key | Role | Action / Purpose |
| :--- | :--- | :--- |
| `workspace.initiated` | Subscribed | Triggers dynamic container & database provisioning workflow. |
| `infrastructure.provisioned` | Published | Signals container readiness to `order-service` for DDL schema migration. |

---

## Local Development & Testing

```bash
# Run unit tests
go test -v ./...

# Repomix packing for LLM analysis
npx repomix
```
