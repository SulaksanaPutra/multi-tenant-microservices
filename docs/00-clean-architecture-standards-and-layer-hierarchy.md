# Microservice Clean Architecture Standards & Layer Hierarchy

This document outlines the architectural standards, layer boundaries, transaction rules, and execution patterns governing all microservices in this workspace.

---

## 1. Microservice Layer Hierarchy & Mental Model

Each microservice follows Clean Architecture boundaries with a predictable, 3-layer mental model:

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

### Layer Responsibilities & Principles

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

## 2. Transaction Boundaries & Invariants

### Rule: No External I/O Inside `txManager.WithTransaction`

`txManager.WithTransaction` must contain **only DB operations**. External network calls (SMTP, HTTP, gRPC) are strictly prohibited inside transaction closures.

#### Why this matters:
- Every millisecond the closure runs, a DB connection and potentially row-level locks are held.
- An SMTP timeout of 30 seconds holds a DB connection for 30 seconds — exhausting the connection pool under load.
- If a network call fails inside a transaction, the rollback undoes all DB writes — on NACK retry, the consumer re-inserts into the inbox (`ON CONFLICT DO NOTHING`) and re-attempts the network call. This is correct behavior only if the external call has **not** already partially succeeded.

#### Pattern:
```go
// CORRECT: Only DB work inside tx; external I/O after commit
var result *DispatchDetails
err := c.txManager.WithTransaction(ctx, func(txCtx context.Context) error {
    var err error
    result, err = service.DoDBWork(txCtx, ...)
    return err
})
if err != nil {
    return err
}
// External I/O executed after commit succeeds
mailer.Send(result.Email)

// WRONG — network I/O inside the transaction closure
c.txManager.WithTransaction(ctx, func(txCtx context.Context) error {
    service.DoDBWork(txCtx, ...)
    mailer.Send(...) // ← PROHIBITED (holds DB connection lock & violates atomicity)
    return nil
})
```

---

## 3. Pattern Specifics & Architectural Exemptions

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

## 4. Inbound & Outbound Delivery Flow Diagrams

### Flow A: Synchronous REST Request (HTTP Endpoint)
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

### Flow B: Inbound Message Consumption (RabbitMQ Consumer)
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

### Flow C: Asynchronous Event Dispatching (Outbox Worker)
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
