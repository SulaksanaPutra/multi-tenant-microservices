# Outbox Worker Concurrency: Eliminating Duplicate Deliveries with Atomic Batch Claiming

*Preventing race conditions and phantom batches across multi-replica workers using PostgreSQL FOR UPDATE SKIP LOCKED.*

---

## 1. The Multi-Replica Race Condition

Once the Transactional Outbox Pattern is in place, dispatching staged messages requires a background polling worker. In single-instance testing, a naive polling loop works as expected:

1. `SELECT * FROM outbox WHERE status = 'PENDING' LIMIT 50`
2. Iterate through records and publish each to RabbitMQ
3. `UPDATE outbox SET status = 'PUBLISHED' WHERE id IN (...)`

However, in production deployments with multiple service replicas running concurrently (Worker A and Worker B), this multi-step loop introduces a race condition:

```text
Timeline (ms)    Worker A (Replica 1)                     Worker B (Replica 2)
  T = 0ms   ────► Runs SELECT (reads Row #1, PENDING)
  T = 10ms  ─────────────────────────────────────────────► Runs SELECT (reads Row #1, still PENDING)
                                                           ▲
                                                           └─ Window of vulnerability: Row #1 not yet updated
  T = 20ms  ────► Publishes Row #1 to RabbitMQ
  T = 25ms  ─────────────────────────────────────────────► Publishes Row #1 to RabbitMQ (Duplicate delivery)
  T = 30ms  ────► Runs UPDATE (Row #1 -> PUBLISHED)
  T = 35ms  ─────────────────────────────────────────────► Runs UPDATE (Row #1 -> PUBLISHED, redundant)
```

### Why This Happens
Between Worker A reading Row #1 and marking it as `PUBLISHED`, Row #1 remains in `PENDING` status inside PostgreSQL. When Worker B queries the outbox during this interval, PostgreSQL returns the same row. Both workers believe they own the event, resulting in duplicate AMQP messages sent to downstream consumers.

---

## 2. The Solution: Atomic Batch Claiming with CTE and `SKIP LOCKED`

To eliminate the gap between reading and updating, we combine the row selection and status transition into a single atomic statement using a PostgreSQL Common Table Expression (CTE) with `FOR UPDATE SKIP LOCKED`.

Implemented in [outbox_repository.go:L111-L133](../user-service/internal/repository/outbox_repository.go#L111-L133):

```sql
WITH claimed AS (
    UPDATE public.outbox
    SET status     = 'PROCESSING',
        claimed_at = NOW()
    WHERE id IN (
        SELECT id
        FROM   public.outbox
        WHERE  status      = 'PENDING'
          AND  event_type  = $1
          AND  retry_count < $2
          AND  (next_retry_at IS NULL OR next_retry_at <= NOW())
        ORDER BY created_at ASC
        LIMIT $3
        FOR UPDATE SKIP LOCKED
    )
    RETURNING id, tenant_id, aggregate_type, aggregate_id, event_type,
              payload, status, retry_count, created_at
)
SELECT * FROM claimed;
```

---

## 3. How `FOR UPDATE SKIP LOCKED` Operates

The query resolves the race condition through three mechanisms:

1. **Atomic State Transition**: Rows are updated from `PENDING` to `PROCESSING` in the exact same statement that selects them. As soon as Worker A's transaction executes this query, those rows are no longer `PENDING`.
2. **Non-Blocking Lock Acquisition (`SKIP LOCKED`)**: When Worker B executes the query concurrently, PostgreSQL checks row-level locks. Instead of blocking and waiting for Worker A's transaction to finish (which would happen with standard `FOR UPDATE`), PostgreSQL skips the locked rows and immediately selects the next available unlocked batch.
3. **Safe Horizontal Scaling**: Multiple worker replicas can poll the same table without coordinating via external distributed locks (such as Redis or Consul). PostgreSQL handles lock mediation directly at the storage engine level.

```text
Timeline (ms)    Worker A (Replica 1)                     Worker B (Replica 2)
  T = 0ms   ────► Executes CTE query
                  (PostgreSQL locks Row #1,
                   transitions status -> PROCESSING)
  T = 10ms  ─────────────────────────────────────────────► Executes CTE query
                                                           (PostgreSQL detects lock on Row #1,
                                                            SKIPS Row #1, claims Row #2 instead)
  T = 20ms  ────► Publishes Row #1 to RabbitMQ
  T = 25ms  ─────────────────────────────────────────────► Publishes Row #2 to RabbitMQ (No collision)
  T = 30ms  ────► Updates Row #1 -> PUBLISHED
  T = 35ms  ─────────────────────────────────────────────► Updates Row #2 -> PUBLISHED
```

---

## 4. Worker Processing Loop

In [outbox_worker.go:L109-L137](../user-service/internal/worker/outbox_worker.go#L109-L137), the worker coordinates batch processing:

```go
func (w *OutboxWorker) processBatch(ctx context.Context) {
    // 1. Atomically claim batch (PENDING -> PROCESSING via FOR UPDATE SKIP LOCKED)
    messages, err := w.outboxRepository.FetchAndClaimBatch(ctx, w.eventType, w.batchSize)
    if err != nil || len(messages) == 0 {
        return
    }

    // 2. Dispatch each message to the message broker
    for _, msg := range messages {
        var evt publisher.UserRegisteredEvent
        _ = json.Unmarshal(msg.Payload, &evt)

        if pubErr := w.publisher.PublishUserRegistered(ctx, evt); pubErr != nil {
            // Publishing failed: record error and schedule retry with backoff
            _ = w.outboxRepository.MarkFailed(ctx, msg.ID, pubErr)
        } else {
            // Publishing succeeded: mark record as PUBLISHED
            _ = w.outboxRepository.MarkPublished(ctx, msg.ID)
        }
    }
}
```

---

## 5. Architectural Invariants & Operational Trade-offs

- **Zero Inter-Worker Coordination**: Workers scale horizontally without distributed lock managers (Redis, Consul). Concurrency mediation is fully delegated to PostgreSQL row locks.
- **Lock Hold Minimization**: The batch query holds row-level locks only for the duration of the CTE execution. Once rows enter `PROCESSING`, locks are released, allowing other transactions to access unrelated records.
- **Batch Sizing Trade-off**: Excessively large batches increase memory usage and per-batch processing duration, while small batches increase query overhead. Batches of 20 to 100 rows provide optimal throughput.
