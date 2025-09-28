# Decoupling Control Plane and Data Plane in Multi-Tenant Microservices

*Structuring workspace-first B2B architectures with asynchronous lifecycle orchestration and independent domain storage.*

---

## 1. The Monolithic Registration Bottleneck

Early iterations of SaaS systems often bundle user registration and workspace setup into a single monolithic workflow:

```text
User Request ──► [Single Service] ──► 1. Create user record
                                  ──► 2. Create tenant record
                                  ──► 3. Provision database schema
                                  ──► 4. Seed initial permissions
                                  ──► 5. Send welcome email
```

In a microservices architecture (`user-service`, `order-service`, `notification-service`), centralizing all of these actions into a single service causes several architectural problems:

1. **Loss of Bounded Contexts**: If `tenant-service` directly creates tables for `order-service`, `tenant-service` must store and maintain `order-service`'s DDL migration files. Any schema change in orders requires updating and deploying tenant-service.
2. **Coupling to Storage Technologies**: `tenant-service` becomes an unwieldy coordinator that requires database drivers and configurations for every storage technology used across the organisation (PostgreSQL, Redis, ClickHouse, etc.).
3. **Synchronous Cascading Failures**: A timeout in a downstream operation (e.g. SMTP email delivery or container cold-start) rolls back or fails the entire user signup.

---

## 2. Workspace-First B2B Architecture

To address this, we decouple the system into a **Control Plane** (`tenant-service`) and multiple independent **Data Planes** (`user-service`, `order-service`, etc.):

```text
                              ┌───────────────────────────────────┐
                              │          CLIENT REQUEST           │
                              │       POST /api/v1/workspaces     │
                              └─────────────────┬─────────────────┘
                                                │
                                                ▼
                              ┌───────────────────────────────────┐
                              │          TENANT-SERVICE           │
                              │      (Passive Control Plane)      │
                              └─────────────────┬─────────────────┘
                                                │
                                     WorkspaceInitiated Event
                                                │
                 ┌──────────────────────────────┴──────────────────────────────┐
                 ▼                                                             ▼
  ┌─────────────────────────────┐                               ┌─────────────────────────────┐
  │        USER-SERVICE         │                               │        ORDER-SERVICE        │
  │    (Identity Data Plane)    │                               │     (Domain Data Plane)     │
  └──────────────┬──────────────┘                               └──────────────┬──────────────┘
                 │                                                             │
        Creates User Identity                                          Provisions DB Schema
                 │                                                     or Dedicated Container
                 │                                                             │
                 │                                                 Reports Routing Metadata
                 │                                                (DSN, host, port, schema)
                 │                                                             │
                 └──────────────────────────────┬──────────────────────────────┘
                                                ▼
                              ┌───────────────────────────────────┐
                              │          TENANT-SERVICE           │
                              │       (State Aggregation)         │
                              └─────────────────┬─────────────────┘
                                                │
                                       WorkspaceReady Event
                                                │
                                                ▼
                              ┌───────────────────────────────────┐
                              │       NOTIFICATION-SERVICE        │
                              │     (Sends Welcome Email)         │
                              └───────────────────────────────────┘
```

---

## 3. Autonomous Data Plane Provisioning

In this model, each domain microservice is the sole authority over its own schema and data:

1. **`tenant-service` Initiates**: Creates a tenant record with status `PENDING` and publishes `workspace.initiated`. It does not know or care how `order-service` stores its tables.
2. **Domain Services React**: `order-service` consumes `workspace.initiated`. It runs its own internal migrations (creating `tenant_xxx` schema or spinning up a dedicated container).
3. **Check-In & Activation**: Once its storage is initialized, `order-service` publishes a check-in event (`tenant.order_db.ready`) containing its routing metadata.
4. **Barrier Completion**: When all required data plane services have reported readiness, `tenant-service` transitions the workspace status from `PENDING` to `ACTIVE` and publishes `workspace.ready`.

---

## 4. Architectural Invariants & Operational Trade-offs

- **Domain Schema Autonomy**: Domain services retain sole ownership over their own DDL migration scripts and storage backends.
- **Asynchronous Fault Tolerance**: Infrastructure provisioning runs asynchronously over AMQP queues, preventing downstream latency spikes from failing client registrations.
- **Barrier Synchronization**: Client activation and notification dispatch are gated until all data plane services report successful storage check-in.
