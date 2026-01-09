# Microservice API Workspace (Multi-Tenant Microservices Architecture)

This workspace demonstrates a **Multi-Tenant Microservices Architecture** supporting both **Shared (Schema-per-Tenant)** and **Dedicated (Database-per-Tenant via Docker)** isolation models, powered by an isolated **`infra-provisioner`** pattern, **Declarative Bootstrapping**, and a **Zero-Trust Control Plane** for secure container orchestration and credential protection.

---

## 1. System Architecture Overview

```text
+-----------------------------------------------------------------------------------+
|                            System Architecture Overview                           |
+-----------------------------------------------------------------------------------+

[ Client App ]
      │  HTTP Requests (POST /api/register, POST /api/orders, GET /api/orders)
      ▼
[ Traefik Gateway :8000 ]
      │
      ├─────► POST /api/register  ────────► [ tenant-service :8082 ]
      │                                             │ (Outbox Write)
      │                                             ▼
      │                                     [ tenantManagerDB ]
      │                                             │
      │                                             ▼ (Publish: workspace.initiated)
      │                                     [ RabbitMQ Broker ]
      │                                             │
      │         ┌───────────────────────────────────┼─────────────────────────┬─────────────────────────┐
      │         ▼                                   ▼                         ▼                         ▼
      └─────► POST / GET /api/orders   [ infra-provisioner ]      [ user-service :8081 ]   [ notification-service :8083 ]
                     │                 (Docker Worker, QoS=1)                 │                         │
                     ▼                         │                              ▼                         ▼
            [ order-service :8084 ]            ▼ Publish:               [ userDB ]            [ notificationDB ]
                     │               infrastructure.provisioned                                [ Mailpit SMTP ]
                     ▼ (Runs SQL Migrations)   │
           [ Tenant Database ] ◄───────────────┘
          (Shared or Dedicated)
                     │
                     ▼ Emits: tenant.order_db.ready
            [ tenant-service ] ──► (Passively Activates Workspace & Upserts Routing Metadata)
```

---

## 2. Workflows & Sequences

### 2.1 Registration & Dynamic Infrastructure Provisioning (`POST /api/register`)

```text
+-----------------------------------------------------------------------------------+
|                            POST /api/register Workflow                            |
+-----------------------------------------------------------------------------------+

[ Client ] 
    │  POST /api/register (email, name, plan)
    ▼
[ tenant-service ] 
    │  1. Save tenant metadata (status: pending)
    │  2. Save workspace.initiated event to Outbox table
    │     ───► (Inside ONE Database Transaction)
    │  3. Return HTTP 202 Accepted to Client
    ▼
[ Outbox Worker ] 
    │  Reads outbox table & publishes workspace.initiated event
    ▼
[ RabbitMQ Queue ] ──► (workspace.initiated)
    │
    ├─────────────────────────────────────────────────┐
    ▼                                                 ▼
[ infra-provisioner ]                             [ user-service ]
    │  Prefetch QoS = 1                               │ Create user profile in userDB
    ├─► Shared Plan:                                  │ Emits: user.created
    │   Pass-through metadata                         │
    ├─► Dedicated Plan:                               ▼
    │   Create Docker container                       [ RabbitMQ Queue ]
    │   (512MB RAM, 0.5 CPU limits)                   │
    │   Declaratively bootstrap domain DBs & roles    │
    │   Poll pg_isready health check                  │
    ▼                                                 │
  Publish: infrastructure.provisioned (No Passwords) │
    ▼                                                 │
[ order-service ]                                     │
    │  Derive DB password via ORDER_SERVICE_SECRET    │
    │  Execute SQL migrations (001_create_orders.sql) │
    ▼                                                 │
  Publish: tenant.order_db.ready (Routing Metadata)   │
    ▼                                                 │
[ tenant-service ]                                    │
    │  Passively updates status -> ACTIVE             │
    │  Stores routing metadata (NO PASSWORDS)         │
    │  Emits: workspace.ready                         │
    ▼                                                 │
[ RabbitMQ Queue ]                                    │
    │                                                 │
    └────────────────────────┬────────────────────────┘
                             │ Both events received (Barrier Sync)
                             ▼
                 [ notification-service ]
                             │ Dispatch Welcome Email via Mailpit
```

---

### 2.2 Orders Workflow (`POST /api/orders` & `GET /api/orders`)

```text
+-----------------------------------------------------------------------------------+
|                        POST / GET /api/orders Workflow                            |
+-----------------------------------------------------------------------------------+

[ Client ]
    │  POST /api/orders or GET /api/orders (Header: tenant-x-id)
    ▼
[ order-service ]
    │  Check PoolRegistry (sync.RWMutex with 3-min TTL & Bounded LRU)
    ├─────────────────────────────────────────┐
    ▼ (Cache Hit)                             ▼ (Cache Miss)
Use existing *sql.DB pool               GET /internal/tenants/:id/infrastructure/order-service
    │                                   Header: X-Internal-Service-Token
    │                                         │
    │                                         ▼ Returns Routing Metadata (host, port, db_name)
    │                                   Derive ORDER_SERVICE_SECRET password in memory & open pool
    │                                         │
    └───────────────────┬─────────────────────┘
                        ▼
    [ Tenant DB (Shared Schema or Dedicated Container) ]
                        │  Execute Query
                        ▼
    [ Response to Client (201 Created or 200 OK) ]
```

---

### 2.3 Infrastructure Availability, Container Rebinding & Routing Invalidation (`tenant.infrastructure_changed`)

```text
+-----------------------------------------------------------------------------------+
|      Infrastructure Availability, Container Rebinding & Cache Invalidation        |
+-----------------------------------------------------------------------------------+

[ Container Event / Admin / Failover ]
    │  Container Rescheduled, IP/Port Changed, or Plan Upgraded/Downgraded
    ▼
[ tenant-service ]
    │  1. Updates tenant infrastructure metadata (host, port, isolation mode)
    │  2. Emits tenant.infrastructure_changed event to Topic Exchange
    ▼
[ RabbitMQ Topic Exchange (company.events) ]
    │
    ├───────────────────────────────┬───────────────────────────────┐
    ▼ (Broadcast Key: tenant.infra_changed)                         ▼
[ Exclusive Queue: amq.gen-1 ]  [ Exclusive Queue: amq.gen-2 ]  [ Exclusive Queue: amq.gen-3 ]
    │                               │                               │
    ▼                               ▼                               ▼
[ order-service-replica-1 ]     [ order-service-replica-2 ]     [ order-service-replica-3 ]
    │ (Purges local Routing &       │ (Purges local Routing &       │ (Purges local Routing &
    │  PoolRegistry caches)         │  PoolRegistry caches)         │  PoolRegistry caches)
    ▼                               ▼                               ▼
 Next request fetches fresh      Next request fetches fresh      Next request fetches fresh
 Rebound Database DSN            Rebound Database DSN            Rebound Database DSN
```

* **New Tenant Registration:** Every `order-service` replica experiences a natural cache miss on its first request and lazily resolves the routing metadata.
* **Infrastructure Rebinding & Plan Changes (Upgrades/Downgrades):** 
  * If a dedicated DB container dies and is rescheduled on a new IP/port by Docker/K8s, OR if a tenant undergoes a plan upgrade/downgrade, `order-service` replicas hold stale DSNs in memory.
  * To prevent routing to dead hosts or split-brain writes, `tenant-service` broadcasts `tenant.infrastructure_changed` over the **`company.events` Topic Exchange** to exclusive anonymous queues, forcing **all** `order-service` replicas to purge their local `RoutingRegistry` and `PoolRegistry` caches in real-time.

---

### 2.4 AMQP Event Contract Matrix

| Event Name | Exchange / Routing Key | Publishing Service | Consuming Service(s) | Payload Purpose & Invariants |
| :--- | :--- | :--- | :--- | :--- |
| `workspace.initiated` | `company.events` / `workspace.initiated` | `tenant-service` | `infra-provisioner`, `user-service` | Triggers container provisioning for dedicated plans & user identity creation. |
| `user.created` | `company.events` / `user.created` | `user-service` | `notification-service` | Tracks user creation for barrier sync prior to dispatching welcome email. |
| `infrastructure.provisioned` | `company.events` / `infrastructure.provisioned` | `infra-provisioner` | `order-service` | Signals container readiness; triggers `order-service` SQL migrations. |
| `tenant.order_db.ready` | `company.events` / `tenant.order_db.ready` | `order-service` | `tenant-service` | Confirms migration success; `tenant-service` activates workspace (`ACTIVE`). |
| `workspace.ready` | `company.events` / `workspace.ready` | `tenant-service` | `notification-service` | Signals complete workspace setup; completes barrier sync for welcome email dispatch. |
| `tenant.infrastructure_changed` | `company.events` / `tenant.infrastructure_changed` | `tenant-service` | `order-service` (all replicas) | Broadcast cache invalidation key to clear stale DB routing/connection pools. |

---

## 3. Microservice Layer Hierarchy & Mental Model

Each microservice follows Clean Architecture boundaries with a predictable, 3-layer mental model:

### The 3 Architectural Layers

```text
┌─────────────────────────────────────────────────────────────────────────────────┐
│                 LAYER 1: ENTRY POINTS / DRIVING ADAPTERS                        │
│                                                                                 │
│      [ HTTP Handler ]              [ AMQP Consumer ]       [ Background Worker ]│
│    (internal/handler)            (internal/consumer)         (internal/worker)  │
└───────────┬────────────────────────────────┼───────────────────────┬────────────┘
            │                                │                       │
            ▼                                ▼                       │
┌──────────────────────────────────────────────────────────────────┐ │
│            LAYER 2: APPLICATION SERVICE CORE                     │ │
│                                                                  │ │
│                   [ Application / Domain Service ]               │ │
│                         (internal/service)                       │ │
└───────────────────┬──────────────────────────────────────────────┘ │
                    │                                                │
                    ▼                                                ▼
┌─────────────────────────────────────────────────────────────────────────────────┐
│               LAYER 3: PERSISTENCE & DRIVEN ADAPTERS                            │
│                                                                                 │
│           [ Repository ]                       [ Publisher Adapter ]            │
│       (internal/repository)                     (internal/publisher)            │
└───────────┬────────────────────────────────────────────┬────────────────────────┘
            │                                            │
            ▼                                            ▼
     [ PostgreSQL DB ]                           [ RabbitMQ Broker ]
```

#### Layer Responsibilities & Principles

1. **Layer 1: Entry Points / Driving Adapters (`handler/`, `consumer/`, `worker/`)**
   - **`handler/`**: Handles incoming HTTP requests (Gin API routes), binds JSON schemas, initiates `txManager.WithTransaction`, delegates to service methods, and writes HTTP responses.
   - **`consumer/`**: Listens for AMQP messages from RabbitMQ queues, initiates `txManager.WithTransaction`, calls `inboxService.ClaimEvent` for deduplication, and ACK/NACKs the channel.
   - **`worker/`**: Runs background loops (e.g. `outboxWorker`), polling PostgreSQL using `SELECT FOR UPDATE SKIP LOCKED` or receiving `.Poke()` signals to dispatch events.
   - **Transaction Ownership**: Transaction boundaries live in Layer 1. Handlers and Consumers own `txManager.WithTransaction(ctx, fn)`, ensuring outer Unit-of-Work boundaries commit before HTTP `200 OK` or AMQP `ACK`.

2. **Layer 2: Application Service Core (`service/`)**
   - Pure, transport-agnostic business logic orchestrating use cases (`WorkspaceService`, `UserService`, `TenantInfrastructureService`, `NotificationService`).
   - Accepts standard `txCtx context.Context` from Layer 1 and passes it down to repositories without holding `*sql.DB` or `*sql.Tx` references directly.
   - Declares consumer-side interfaces for dependencies (`TenantRepository`, `OutboxRepository`).

3. **Layer 3: Persistence & Driven Adapters (`repository/`, `publisher/`)**
   - **`repository/`**: Executes raw SQL queries against PostgreSQL. Dynamically extracts `DBExecutor` (`*sql.Tx` if active, fallback to `*sql.DB` pool) via `txcontext.GetExecutor(ctx, r.dbClient)`. Always returns pure `domain.*` entities.
   - **`publisher/`**: Serializes domain event payloads and publishes frames to RabbitMQ exchanges.

---

### Intentional Exemption: `MigrationService` in `order-service`

`order-service/internal/service/migration_service.go` is the **only** intentional exception to the Layer 2 no-database-connection rule.

**Why:** DDL schema migrations for new tenants must run against an *arbitrary, runtime-derived tenant DSN* — not the service's own pre-established connection pool. This cannot be delegated to a standard `txcontext.GetExecutor` repository because:
- The target database does not exist in any pool yet.
- DDL operations like `CREATE SCHEMA` must run outside a transaction on some databases.
- The migration SQL may contain a `-- tx: false` annotation requiring non-transactional execution.

`MigrationService` opens an ephemeral, self-closing connection per migration call and is consumed through a **consumer-side interface** (`type MigrationService interface { MigrateTenantDB(...) error }`) — no DB primitives leak to Layer 1. It is architecturally a narrow infrastructure utility, not a domain service. It should be treated as such during code review.

---

### Barrier Sync Consumer Pattern (`notification-service`)

When a consumer implements a **barrier sync** (must wait for N independent events before acting), the inbox is used as first-class business input — not just a deduplication guard. The Layer 1 consumer owns both steps:

```text
Consumer (Layer 1)
  └─ txManager.WithTransaction
       ├─ 1. inboxService.ClaimEvent(txCtx, inboxInput)      ← guard: deduplicates atomically
       ├─ 2. inboxService.GetBarrierEvents(txCtx, tenantID)  ← reads full inbox state (consistent in tx)
       └─ 3. notificationService.ProcessEventAndTrySendWelcome(txCtx, input, events)
                                                              ← service receives events as DATA
                                                              ← writes pending audit log, returns details
  (Transaction commits — DB connection released)
  └─ 4. mailer.SendWelcomeEmail(details.RecipientEmail, ...)  ← AFTER commit, outside tx
```

This pattern ensures:
- `NotificationService` is a pure business service with no inbox repository dependency.
- The barrier state read is consistent with the `ClaimEvent` write (same transaction).
- External I/O (SMTP) never holds an open DB transaction.

---

### Rule: No External I/O Inside `txManager.WithTransaction`

`txManager.WithTransaction` must contain **only DB operations**. External network calls (SMTP, HTTP, gRPC) are strictly prohibited inside transaction closures.

**Why this matters:**
- Every millisecond the closure runs, a DB connection and potentially row-level locks are held.
- An SMTP timeout of 30 seconds holds a DB connection for 30 seconds — exhausting the connection pool under load.
- If a network call fails inside a transaction, the rollback undoes all DB writes — on NACK retry, the consumer re-inserts into the inbox (`ON CONFLICT DO NOTHING`) and re-attempts the network call. This is correct behavior only if the external call has **not** already partially succeeded.

**Pattern:**
```go
// CORRECT
var result *DispatchDetails
c.txManager.WithTransaction(ctx, func(txCtx context.Context) error {
    // Only DB work here
    result, err = service.DoDBWork(txCtx, ...)
    return err
})
// External I/O after commit
mailer.Send(result.Email)

// WRONG — network I/O inside the transaction closure
c.txManager.WithTransaction(ctx, func(txCtx context.Context) error {
    service.DoDBWork(txCtx, ...)
    mailer.Send(...) // ← PROHIBITED
    return nil
})
```

---

### Inbound & Outbound Delivery Flow Diagrams

#### Flow A: Synchronous REST Request (HTTP Endpoint)
```text
Client HTTP POST /api/register
  │
  ▼
[ WorkspaceHandler ]           (internal/handler)
  │ 1. Opens Tx: txManager.WithTransaction(ctx, ...)
  ▼
[ WorkspaceService ]           (internal/service)
  │ 2. Validates & executes domain business logic
  ├──────────────────────────┐
  ▼                          ▼
[ TenantRepository ]   [ OutboxRepository ]    (internal/repository)
  │                          │
  └──────────┬───────────────┘
             │ 3. Writes Tenant & Outbox records in SAME Tx
             ▼
       [ PostgreSQL ]
             │ 4. Tx Commit succeeds!
             ▼
  [ WorkspaceHandler writes HTTP 202 Accepted & pokes outboxWorker.Poke() ]
```

#### Flow B: Inbound Message Consumption (RabbitMQ Consumer)
```text
RabbitMQ Message (workspace.initiated)
  │
  ▼
[ WorkspaceInitiatedConsumer ] (internal/consumer)
  │ 1. Opens Tx: txManager.WithTransaction(ctx, ...)
  ▼
[ InboxService.ClaimEvent ]    (internal/service)
  │ 2. Deduplicates event_id atomically inside txCtx
  ▼
[ Domain / Provisioner Service ] (internal/service)
  │ 3. Executes downstream business logic
  ▼
[ TenantInfraRepository ]      (internal/repository)
  │ 4. Persists infrastructure state
  ▼
  [ PostgreSQL ]
  │ 5. Tx Commit succeeds!
  ▼
  [ Consumer Acks message to RabbitMQ ]
  (Note: If step 3 fails, Tx rolls back Inbox claim atomically & Consumer NACKs for retry)
```

#### Flow C: Asynchronous Event Dispatching (Outbox Worker)
```text
Background Timer Ticker / .Poke()
  │
  ▼
[ OutboxWorker ]               (internal/worker)
  │ 1. Fetches pending outbox batch (SELECT FOR UPDATE SKIP LOCKED)
  ▼
[ OutboxRepository ]           (internal/repository)
  │
  ▼
[ TenantEventPublisher ]       (internal/publisher)
  │ 2. Serializes AMQP payload & publishes to Exchange
  ▼
  [ RabbitMQ Exchange ]
  │ 3. On success: OutboxWorker marks status = 'PUBLISHED' in DB
```

---

## 4. Architecture Deep-Dive Documentation Index

This repository contains comprehensive technical design deep-dives located in the [`docs/`](docs/) directory:

| # | Document Title | Focus Area |
| :-: | :--- | :--- |
| 1 | [What Happens If The Broadcaster Breaks?](docs/1-what-happens-if-the-broadcaster-breaks-and-how-do-we-retry.md) | Outbox Pattern, At-Least-Once Delivery & Retry Loops |
| 2 | [How Does Phantom Batch Duplicate Delivery Happen?](docs/2-how-does-the-phantom-batch-duplicate-delivery-happen-and-how-do-we-fix-it.md) | Inbox Pattern, Deduplication Barriers & Consumer Idempotency |
| 3 | [What If The Database Crashes After RabbitMQ Succeeds?](docs/3-what-if-the-database-crashes-after-rabbitmq-succeeds-the-idempotent-consumer.md) | Atomic Transactions & Transactional Inbox Handlers |
| 4 | [What Happens If Tenant Schema Creation Fails Midway?](docs/4-what-happens-if-tenant-schema-creation-fails-midway-transactional-ddl.md) | Transactional DDL, Migration Rollbacks & Schema Safety |
| 5 | [How Do We Scale Multi-Tenancy from Shared Schema to Dedicated Database?](docs/5-how-do-we-scale-multi-tenancy-from-shared-schema-to-dedicated-database-hybrid-duality.md) | Hybrid Multi-Tenant Duality & Plan Upgrades |
| 6 | [How Do We Decouple Control Plane & Data Plane?](docs/6-how-do-we-decouple-control-plane-and-data-plane-workspace-first-b2b-architecture.md) | Passive Control Plane Registry & Zero-Boot Connection Pools |
| 7 | [How Do We Manage Database Transactions and Domain Invariants?](docs/7-how-do-we-manage-database-transactions-and-domain-invariants-outer-layer-unit-of-work.md) | Outer-Layer Unit of Work, Transaction Context & Domain Isolation |
| 8 | [How Do We Isolate Container Orchestration & Prevent Host Takeover?](docs/8-how-do-we-isolate-container-orchestration-and-prevent-host-takeover-infra-provisioner-pattern.md) | `infra-provisioner` Pattern, Docker Socket Isolation & HMAC Credentials |
| 9 | [How Do We Prevent Lateral Movement & Secure the Control Plane?](docs/9-how-do-we-prevent-lateral-movement-and-secure-the-control-plane-zero-trust-metadata-sanitization.md) | Control Plane Metadata Sanitization, Zero-Trust Inter-Service Auth & Ghost Route Removal |
| 10 | [How Do We Isolate Domain Database Secrets Without OCP Violations?](docs/10-how-do-we-isolate-domain-database-secrets-without-ocp-violations-declarative-bootstrapping.md) | Declarative Configuration Bootstrapping, PostgreSQL Role Least Privilege & Root Key Trap Prevention |
| 11 | [How Do We Eliminate Cache Stampedes and Decouple Data Plane Routing?](docs/11-how-do-we-eliminate-cache-stampedes-and-decouple-data-plane-routing-singleflight-and-in-memory-materialized-view.md) | Request Coalescing (`singleflight`), Context Shielding & Local In-Memory `RoutingRegistry` Materialized View |
| 12 | [How Do We Prevent PostgreSQL Transaction Abortion?](docs/12-how-do-we-prevent-postgresql-transaction-abortion-and-maintain-clean-outer-layer-unit-of-work.md) | Outer-Layer Consumer Unit-of-Work, PostgreSQL Aborted Transaction Trap (`23505`) & `ON CONFLICT DO NOTHING` |
| 13 | [How Do We Prevent Horizontal Split-Brain Cache Invalidation and Socket Sprawl?](docs/13-how-do-we-prevent-horizontal-split-brain-cache-invalidation-and-multi-tenant-connection-sprawl.md) | Fanout Broadcast Topology, Competing Consumer DDL Migration Guardrail, Double-Checked Reaper Sweeps & Connection Pool Tuning |
| 14 | [How Do We Prevent Transient Network Split-Brain from AMQP Cache Invalidation Loss?](docs/14-how-do-we-prevent-transient-network-split-brain-cache-invalidation-loss.md) | Two-Layer Reconnect Driver, `NotifyReconnect` Signal, `PurgeAll` Cache Barriers & Re-Binding Topology |
| 15 | [When the Broker Goes Silent, Does Your Service Tell the Truth?](docs/15-when-the-broker-goes-silent-does-your-service-tell-the-truth.md) | Context Lifecycles, Cache Miss Throttling, Singleflight & Load Shedding |
| 16 | [How Do We Safely Manage Multi-Tenant Database Duality Without Data Leakage or Connection Sprawl?](docs/16-how-do-we-safely-manage-multi-tenant-database-duality-without-cross-tenant-data-leakage-or-connection-sprawl.md) | Constructor Arity, Dynamic DSN Resolution, Bounded LRU Connection Pooling & Clean Architecture Isolation |

---

## 5. Directory Structure

```text
microservice-api/
├── README.md                     # Workspace & Architecture Documentation
│
├── infra-provisioner/            # Isolated Infrastructure Provisioning Worker
│   ├── cmd/main.go               # Entrypoint & RabbitMQ consumer
│   ├── internal/
│   │   ├── crypto/               # HMAC-SHA256 deterministic credential derivation
│   │   ├── docker/               # Docker SDK client with 512MB RAM / 0.5 CPU limits & pg_isready
│   │   └── consumer/             # workspace.initiated consumer (QoS prefetch = 1)
│   └── Dockerfile
│
├── tenant-service/               # Control-Plane Tenant Management & Outbox Service
│   ├── cmd/main.go               # Port 8082 - Control Plane & Outbox Worker
│   ├── internal/
│   │   ├── middleware/           # InternalAuthMiddleware (X-Internal-Service-Token)
│   │   └── repository/           # Control plane metadata-only repository
│   └── Dockerfile
│
├── user-service/                 # User Identity Service
│   ├── cmd/main.go               # Port 8081 - User Profile & Event Consumer
│   └── Dockerfile
│
├── order-service/                # Dynamic Multi-Tenant Data-Plane Service
│   ├── cmd/main.go               # Port 8084 - Orders API & Migration Consumer
│   ├── internal/
│   │   ├── crypto/               # HMAC-SHA256 password derivation
│   │   ├── infrastructure/       # Zero-Trust TenantDB Resolver
│   │   └── registry/             # PoolRegistry with 15-minute TTL eviction
│   └── Dockerfile
│
├── notification-service/         # Async Notification Worker
│   ├── cmd/main.go               # Port 8083 - Mailpit Dispatcher & Audit Logger
│   └── Dockerfile
│
├── infrastructure/               # Shared Infrastructure & Docker Topology
│   ├── init.sql                  # Base database initialization scripts (Sanitized tenant_services schema)
│   ├── docker-compose.yml        # Postgres, RabbitMQ, Mailpit, Traefik, Infra-Provisioner, Web-UI
│   └── web-ui/                   # Functional Web UI
│
├── docs/                         # Architectural Deep-Dives & Technical Design Challenges
│   └── 10-how-do-we-isolate-domain-database-secrets...md
│
└── e2e-tests/                    # Automated Integration Tests
    └── register_e2e_test.go      # Dynamic registration & order flow test suite
```

---

## 6. Port Map & Component Dashboard

| Service / Tool | Port | Endpoint / Dashboard | Description |
| :--- | :--- | :--- | :--- |
| **Traefik Gateway** | `8000` | `http://localhost:8000` | Gateway entrypoint for APIs & Web UI |
| **Flow Demo Web UI** | `8000` | `http://localhost:8000/` | Web UI for tenant registration & orders |
| **tenant-service** | `8082` | `tenant-service:8082` | Control plane registry & registration API |
| **order-service** | `8084` | `order-service:8084` | Orders data plane & migration consumer |
| **user-service** | `8081` | `user-service:8081` | User profile service |
| **notification-service** | `8083` | `notification-service:8083` | Email notification worker |
| **infra-provisioner** | *None* | *Internal Worker* | Docker container provisioner (QoS=1, isolated socket) |
| **RabbitMQ Management**| `15672` | `http://localhost:15672` | Queue dashboard (`guest` / `guest`) |
| **Mailpit Dashboard** | `8025` | `http://localhost:8025` | Mock email inbox UI |

---

## 7. How to Run & Stop the Application

### Starting Infrastructure & Services

```bash
# 1. Start Shared Infrastructure & Infra Provisioner
(cd infrastructure && docker compose up -d)

# 2. Start Microservices
(cd tenant-service && docker compose up -d --build) && \
(cd user-service && docker compose up -d --build) && \
(cd order-service && docker compose up -d --build) && \
(cd notification-service && docker compose up -d --build)

# 3. Run E2E Integration Tests
(cd e2e-tests && CGO_ENABLED=0 go test -v ./...)
```

### Stopping All Services

```bash
(cd notification-service && docker compose down) && \
(cd order-service && docker compose down) && \
(cd user-service && docker compose down) && \
(cd tenant-service && docker compose down) && \
(cd infrastructure && docker compose down)
```
