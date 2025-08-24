# Distributed Dual-Write Failures and the Transactional Outbox Pattern

*Handling broker disconnects, process crashes, and reliable event publishing using PostgreSQL and RabbitMQ.*

---

## 1. The Dual-Write Problem

A common pitfall when designing event-driven microservices is writing to the database and publishing to a message broker sequentially:

```go
// 1. Write business entity to primary database
db.Exec("INSERT INTO users (id, email, name) VALUES ...")

// 2. Publish domain event to broker
rabbitmq.Publish("user.events", eventPayload)
```

In development or low-load environments, this often looks fine. But under real network conditions, this pattern contains a fundamental consistency flaw: **the two operations span different storage engines without a distributed transaction (2PC).**

Consider the failure modes:
1. **Database succeeds, broker publish fails:**
   If the application crashes, the network drops, or RabbitMQ is temporarily unreachable right after `db.Exec`, the database transaction is already committed. Downstream services (e.g., provisioning workspace databases or sending notifications) never learn that the user was created. The data remains silently inconsistent.
2. **Broker publish succeeds, database commit fails:**
   If we attempt to publish first and save to the database second, any database failure (unique constraint violation, deadlock, or timeout) produces a phantom event. Downstream consumers react to an event for an entity that never existed in the database.

Because standard network calls cannot be atomically bound to local relational database transactions, neither sequence guarantees consistency.

---

## 2. Implementing the Transactional Outbox Pattern

To eliminate this consistency gap without the operational overhead and blocking locks of Two-Phase Commit (2PC), we use the **Transactional Outbox Pattern**.

Instead of dispatching an AMQP message over the network inside the HTTP request path, the event payload is inserted into a dedicated `outbox` table in PostgreSQL using the **exact same database transaction (`*sql.Tx`)** as the business mutation.

An example from [user_service.go:L65-122](../user-service/internal/service/user_service.go#L65-L122):

```go
// 1. Begin database transaction
tx, err := s.dbClient.BeginTx(ctx, nil)
if err != nil {
    return nil, fmt.Errorf("failed to start database transaction: %w", err)
}
defer tx.Rollback()

// 2. Persist User Record
if err := s.userRepository.CreateUser(ctx, tx, userObj); err != nil {
    return nil, err
}

// 3. Persist Tenant Record
if err := s.tenantRepository.CreateTenant(ctx, tx, tenantObj); err != nil {
    return nil, err
}

// 4. Stage Domain Event Payload inside Transactional Outbox table
outboxMsg := repository.OutboxMessage{
    ID:            outboxID,
    TenantID:      &tenantID,
    AggregateType: "USER",
    AggregateID:   userID,
    EventType:     "user.registered",
    Payload:       payloadBytes,
    Status:        "PENDING",
}
if err := s.outboxRepository.CreateOutboxMessage(ctx, tx, outboxMsg); err != nil {
    return nil, fmt.Errorf("failed to stage outbox event in transaction: %w", err)
}

// 5. Commit atomically: user, tenant, and outbox record commit together
if err := tx.Commit(); err != nil {
    return nil, fmt.Errorf("failed to commit transaction: %w", err)
}
```

With this approach:
- If the database transaction aborts, the outbox record rolls back with it. No phantom events are ever emitted.
- Once committed, the event record is durable on disk. An asynchronous background process can retry publishing until the broker acknowledges receipt.

---

## 3. Schema Design and Partial Indexing

The outbox table definition in [init.sql:L40-L54](../broker/init.sql#L40-L54) includes several fields intended for production resiliency:

```sql
CREATE TABLE IF NOT EXISTS public.outbox (
    id VARCHAR(255) PRIMARY KEY,
    tenant_id VARCHAR(255),
    aggregate_type VARCHAR(255) NOT NULL,
    aggregate_id VARCHAR(255) NOT NULL,
    event_type VARCHAR(255) NOT NULL,
    payload JSONB NOT NULL,
    status VARCHAR(50) NOT NULL DEFAULT 'PENDING',
    retry_count INT NOT NULL DEFAULT 0,
    last_error VARCHAR(500),
    next_retry_at TIMESTAMP WITH TIME ZONE,
    claimed_at TIMESTAMP WITH TIME ZONE,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    processed_at TIMESTAMP WITH TIME ZONE
);
```

### Key Column Details
- **`payload` (`JSONB`)**: Stores the full serialized event payload at the time of creation. This avoids re-querying domain tables later, ensuring consumers receive the exact state of the entity at the moment the event occurred.
- **`claimed_at` (`TIMESTAMP`)**: Tracks when a background worker picked up the event, allowing stale processing locks to be identified and recovered if a worker process crashes mid-flight.
- **`next_retry_at` (`TIMESTAMP`)**: Used for scheduling retries with exponential backoff rather than hammering a degraded broker in a tight loop.
- **`last_error` (`VARCHAR(500)`)**: Captures the failure reason for monitoring and debugging without requiring manual log correlation.

### Indexing Active Rows
In a busy system, the outbox table accumulates historical records over time. Creating an index across all statuses would waste storage and cache memory on rows that are already completed.

To keep queries fast, we define a **partial index** in [init.sql:L62-L63](../broker/init.sql#L62-L63):

```sql
CREATE INDEX IF NOT EXISTS idx_outbox_pending ON public.outbox(event_type, next_retry_at, created_at)
    WHERE status IN ('PENDING', 'PROCESSING');
```

Because completed (`PUBLISHED`) records are excluded from this index, lookups for pending batches remain fast regardless of table size.

---

## 4. Background Dispatching and Edge Cases

Staging the event is only half the problem. A background process (`outboxWorker`) must reliably pick up pending records and publish them to RabbitMQ.

```text
Database Tx Committed
  │
  ├─► Immediate: Handler calls worker.Poke() (dispatches without waiting for poll interval)
  │
  └─► Fallback: Ticker polls every 5s (catches events missed during restarts or restarts)
```

### Edge Case 1: Worker Process Crashes Mid-Batch
If a worker crashes while publishing a batch:
- Rows were marked `PROCESSING` with `claimed_at = NOW()`.
- A recovery sweeper runs periodically: if `claimed_at < NOW() - INTERVAL '30 seconds'`, the claim is considered expired, and the status resets to `PENDING` for redelivery.

### Edge Case 2: RabbitMQ Is Down
If publishing fails:
- The worker increments `retry_count`.
- Calculates an exponential backoff (e.g. 2^(retry_count) seconds) and sets `next_retry_at`.
- Transitions the row back to `PENDING`.

### Edge Case 3: Poison Pill Events
If an event fails repeatedly (e.g. invalid payload or missing configuration), retrying indefinitely can block subsequent events or waste resources. Once `retry_count` reaches a configured ceiling (e.g., 5 retries), the worker marks the row `FAILED` and logs an alert.

---

## 5. Architectural Invariants & Operational Trade-offs

- **At-Least-Once Delivery**: The outbox pattern guarantees that committed events are never lost. However, retries mean downstream consumers can receive duplicates. Consumers must implement idempotency.
- **Storage Growth and Archiving**: Completed outbox rows accumulate over time. Production databases require a scheduled maintenance job to prune or partition rows in `PUBLISHED` status.
- **Index Efficiency**: The partial index ensures lookups for pending rows remain constant-time, preventing outbox polling from impacting primary database performance.
