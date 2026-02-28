# Clean Architecture Standards and Layer Boundaries

*Core architectural boundaries, layer separation rules, transaction management, and execution flows across services.*

---

## 1. Layer Hierarchy and Mental Model

Every microservice adheres to a strict three-layer hierarchy:

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

### Responsibilities by Layer

1. **Layer 1: Entry Points and Driving Adapters (`handler/`, `consumer/`, `worker/`)**
   - **`handler/`**: Receives incoming HTTP requests (Gin router), binds and validates JSON schemas, manages the top-level transaction boundary via `txManager.WithTransaction`, orchestrates domain services, and renders HTTP responses.
   - **`consumer/`**: Consumes AMQP messages from RabbitMQ queues, initiates `txManager.WithTransaction`, coordinates event deduplication via `inboxService.ClaimEvent`, and manages message ACK/NACK signaling on the channel.
   - **`worker/`**: Drives background loops (such as `outboxWorker`), either polling PostgreSQL via `SELECT FOR UPDATE SKIP LOCKED` or handling `.Poke()` signals to dispatch staged events. Workers bypass Layer 2 by design when executing pure I/O coordination.
   - **Outbound Ports Manifest (`interfaces.go`)**: Every Layer 1 package must define its outbound dependencies inside an `interfaces.go` file (with accompanying unit tests in `interfaces_test.go`).
   - **Transaction Ownership**: Cross-domain choreography and transaction lifecycles belong strictly in Layer 1. Handlers, consumers, and workers control `txManager.WithTransaction(ctx, fn)` to ensure the outer Unit-of-Work commits or aborts cleanly.

2. **Layer 2: Application Service Core (`service/`)**
   - Encapsulates pure, transport-independent business logic scoped to a single domain aggregate (e.g., `WorkspaceService`, `UserService`, `TenantInfrastructureService`, `NotificationService`).
   - **Domain Purity and Boundary Isolation**: Services must not import or invoke peer domain services or foreign repositories. All cross-domain coordination must be driven from Layer 1.
   - Accepts a standard `context.Context` carrying an active transaction (`txCtx`) from Layer 1 and passes it down to repositories. Services do not maintain direct references to `*sql.DB` or `*sql.Tx`.
   - Consumer-side interfaces for repositories must be declared directly in the consuming service file.

3. **Layer 3: Persistence and Driven Adapters (`repository/`, `publisher/`)**
   - **`repository/`**: Executes SQL statements against PostgreSQL. Obtains the active `DBExecutor` (`*sql.Tx` when transactional, otherwise fallback to `*sql.DB`) via `txcontext.GetExecutor(ctx, r.dbClient)`. Repositories always map query results to pure domain entities.
   - **`publisher/`**: Marshals domain event payloads into AMQP byte frames and publishes them to target RabbitMQ exchanges.

---

## 2. Transaction Boundaries and Invariants

### Rule: No External I/O Inside `txManager.WithTransaction`

Database transaction closures managed by `txManager.WithTransaction` must be restricted to database operations only. Network calls (such as SMTP dispatch, third-party HTTP APIs, or external gRPC calls) are prohibited inside transaction blocks.

#### Practical Reasons:
- Every millisecond spent inside an active transaction block holds an open database connection from the pool, along with any row-level locks acquired during writes.
- If an external call stalls (for example, a 15-second SMTP connect timeout), that database connection remains locked and unavailable to other requests, leading to pool starvation under moderate concurrency.
- Network failures occurring inside a transaction trigger a rollback. If an external API call had already succeeded upstream before the failure, the local state rolls back while the remote state remains mutated, resulting in inconsistent side effects.

#### Implementation Pattern:
```go
// Correct pattern: database operations inside transaction, external network I/O outside
var result *DispatchDetails
err := c.txManager.WithTransaction(ctx, func(txCtx context.Context) error {
    var err error
    result, err = service.DoDBWork(txCtx, input)
    return err
})
if err != nil {
    return err
}

// Network I/O runs only after the database transaction has successfully committed
if err := mailer.Send(result.Email); err != nil {
    // Handle transient mailer error (e.g. log, queue for retry)
}
```

Antipattern to avoid:
```go
// Incorrect: network calls inside transaction closure
c.txManager.WithTransaction(ctx, func(txCtx context.Context) error {
    service.DoDBWork(txCtx, input)
    mailer.Send(...) // Antipattern: holds DB pool connection during network latency
    return nil
})
```

---

## 3. Specific Patterns and Explicit Architecture Exemptions

### Documented Exemption: `MigrationService` in `order-service`

The implementation in `order-service/internal/service/migration_service.go` is an intentional exception to the rule that Layer 2 services do not maintain direct database connections.

**Rationale:** Tenant database schema provisioning runs DDL statements against dynamic, tenant-specific DSNs resolved at runtime, rather than against the service's static connection pool. This cannot use standard `txcontext.GetExecutor` repositories because:
- The target tenant database is provisioned on demand and is not pre-registered in the connection pool.
- PostgreSQL DDL statements (like `CREATE SCHEMA`) may need dedicated execution modes, or migration scripts may include `-- tx: false` directives requiring non-transactional execution.

`MigrationService` initializes short-lived connections dedicated to the migration lifecycle and exposes this capability through a consumer-side interface (`type MigrationService interface { MigrateTenantDB(...) error }`). Database connection primitives do not leak into calling layers.

---

### Barrier Synchronization Consumer Pattern (`notification-service`)

When an event consumer must wait for multiple independent upstream events before executing a side effect (such as waiting for both workspace creation and database provisioning before sending a welcome email), the inbox table is used as state storage:

```text
Consumer (Layer 1)
  └─ txManager.WithTransaction
       ├─ 1. inboxService.ClaimEvent(txCtx, inboxInput)      (Idempotency: claims event atomically)
       ├─ 2. inboxService.GetBarrierEvents(txCtx, tenantID)  (Reads collected barrier state within tx)
       └─ 3. notificationService.ProcessEventAndTrySendWelcome(txCtx, input, events)
                                                               (Evaluates business conditions)
                                                               (Writes pending notification audit record)
  (Transaction commits - DB connection released)
  └─ 4. mailer.SendWelcomeEmail(details.RecipientEmail, ...)  (Dispatches SMTP outside of tx)
```

This layout guarantees that:
- `NotificationService` remains focused on domain business rules without managing raw inbox database queries directly.
- The barrier evaluation runs against a consistent transactional snapshot alongside `ClaimEvent`.
- SMTP network latency does not tie up PostgreSQL connection pool resources.

---

## 4. Request and Event Lifecycles

### Synchronous REST Endpoint (Workspace Creation)
```text
Client HTTP POST /api/v1/workspaces
  │
  ▼
[ WorkspaceHandler ]           (internal/handler)
  │ 1. Initiates transaction: txManager.WithTransaction(ctx, ...)
  ▼
[ WorkspaceService ]           (internal/service)
  │ 2. Applies domain validation and business invariants
  ├──────────────────────────┐
  ▼                          ▼
[ TenantRepository ]   [ OutboxRepository ]    (internal/repository)
  │                          │
  └──────────┬───────────────┘
             │ 3. Inserts tenant and stages outbox event within the SAME transaction
             ▼
       [ PostgreSQL ]
             │ 4. Transaction commits successfully
             ▼
  [ WorkspaceHandler responds with HTTP 202 Accepted and signals outboxWorker.Poke() ]
```

### Inbound AMQP Event Handling (Tenant Provisioning)
```text
RabbitMQ Message (workspace.initiated)
  │
  ▼
[ WorkspaceInitiatedConsumer ] (internal/consumer)
  │ 1. Initiates transaction: txManager.WithTransaction(ctx, ...)
  ▼
[ InboxService.ClaimEvent ]    (internal/service)
  │ 2. Deduplicates event_id using inbox table within txCtx
  ▼
[ ProvisionerService ]         (internal/service)
  │ 3. Executes domain provisioning workflow
  ▼
[ TenantInfraRepository ]      (internal/repository)
  │ 4. Updates infrastructure state in database
  ▼
  [ PostgreSQL ]
  │ 5. Transaction commits
  ▼
  [ Consumer ACKs message on RabbitMQ channel ]
  (Note: if any step fails, the transaction rolls back the inbox claim and the consumer NACKs for retry)
```

### Asynchronous Event Dispatching (Outbox Worker)
```text
Background Polling Ticker or .Poke() Signal
  │
  ▼
[ OutboxWorker ]               (internal/worker)
  │ 1. Claims pending batch via SELECT FOR UPDATE SKIP LOCKED
  ▼
[ OutboxRepository ]           (internal/repository)
  │
  ▼
[ TenantEventPublisher ]       (internal/publisher)
  │ 2. Marshals event payload to JSON and publishes to RabbitMQ exchange
  ▼
  [ RabbitMQ Exchange ]
  │ 3. Upon publisher confirmation, worker marks record as PUBLISHED
```

---

## 5. Architectural Invariants & Operational Trade-offs

- **Layer Inversion Invariant**: Driving adapters (Layer 1) control top-level transaction boundaries and declare outbound interfaces (`interfaces.go`). Domain services (Layer 2) remain transport-agnostic and never import peer domain services or foreign repositories.
- **Connection Hold Bounds**: Non-database I/O inside `WithTransaction` is prohibited. The system trades off multi-phase orchestration complexity for pool connection safety and bounded lock durations.
- **Barrier State Consistency**: When waiting for multiple distributed events, barrier queries run inside the same database transaction as the inbox claim to prevent stale state reads.
