# Consumer Idempotency and the Transactional Inbox Pattern

*Handling post-publish worker crashes, duplicate broker delivery, and building crash-safe consumers.*

---

## 1. The Post-Publish Failure Window

The atomic batch claiming mechanism (`FOR UPDATE SKIP LOCKED`) prevents two healthy outbox workers from picking up the same records simultaneously. However, in distributed architectures, another failure mode exists: **a crash occurring after publishing but before recording completion in the database.**

Consider this sequence of events:

```text
Timeline          Outbox Worker Execution
─────────────────────────────────────────────────────────────────────
  T = 0ms    Executes CTE query; Row #1 marked PROCESSING.
  T = 10ms   Publishes Row #1 to RabbitMQ exchange.
             RabbitMQ confirms receipt (message is now queued in broker).
  T = 11ms   Worker attempts to execute:
             UPDATE outbox SET status = 'PUBLISHED' WHERE id = 1;
             [CRASH] The container runs out of memory, is killed, or network drops.
  T = 30s    Worker container restarts.
             The claim recovery sweeper detects Row #1 has been stuck
             in PROCESSING longer than the lease duration (30 seconds).
             Status is reset to PENDING.
  T = 31s    Worker claims Row #1 again and re-publishes to RabbitMQ.
             Downstream queues now contain a duplicate message.
```

### Why Publisher Fixes Cannot Prevent This
Because publishing to RabbitMQ and updating PostgreSQL are separate network actions, there is an unavoidable window where the broker has received the message, but the publisher database has not yet recorded that success.

Under network partitions or process terminations, the publisher must choose between two options:
1. **At-most-once**: Do not retry if unsure. Risk losing messages permanently.
2. **At-least-once**: Retry until confirmation is received. Risk sending duplicates.

In transactional systems, losing messages is unacceptable. Therefore, publishers must operate under **at-least-once delivery**, which places the responsibility for handling duplicates squarely on the **consumer**.

---

## 2. Consumer-Side Deduplication Strategies

Different consumer operations require different deduplication approaches:

### Strategy A: Natural Idempotency via Unique Constraints

For simple relational inserts, the receiving database table may already have a natural business key (such as `user_id` or `tenant_id`).

In `tenant-service`, workspace provisioning uses unique constraints:

```sql
INSERT INTO public.tenants (id, name, slug, status, created_at)
VALUES ($1, $2, $3, 'ACTIVE', NOW())
ON CONFLICT (id) DO NOTHING;
```

If a duplicate `user.registered` event arrives:
- The first event inserts the row.
- The second event triggers `ON CONFLICT (id) DO NOTHING`. PostgreSQL discards the insert cleanly without throwing an unhandled error.
- The consumer considers the event handled and ACKs the message.

---

### Strategy B: The Transactional Inbox Pattern

Natural unique constraints do not work when:
- The consumer performs non-idempotent operations (such as appending audit logs, incrementing counters, or provisioning tenant schemas).
- The operation involves multiple tables where partial updates could leave data in an inconsistent state.

In these cases, we implement the **Transactional Inbox Pattern**.

The consumer maintains an `inbox` table that tracks processed message IDs within the **same local database transaction** as the business changes:

```sql
CREATE TABLE IF NOT EXISTS public.inbox (
    message_id VARCHAR(255) PRIMARY KEY,
    consumer_name VARCHAR(255) NOT NULL,
    event_type VARCHAR(255) NOT NULL,
    payload JSONB,
    processed_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);
```

#### Consumer Workflow with Inbox Table:

```text
RabbitMQ Delivery (event_id, payload)
  │
  ▼
[ Consumer.HandleMessage ]
  │
  ├─► 1. Begin local database transaction (sql.Tx)
  │
  ├─► 2. Execute:
  │      INSERT INTO public.inbox (message_id, consumer_name, event_type)
  │      VALUES ($1, $2, $3)
  │      ON CONFLICT (message_id) DO NOTHING;
  │
  ├─► 3. Check rows affected:
  │      - If 0: Event was ALREADY PROCESSED. Roll back tx, ACK message to broker.
  │      - If 1: Event is NEW. Proceed to Step 4.
  │
  ├─► 4. Execute domain business logic (e.g., create workspace schema, update records)
  │
  ├─► 5. Commit database transaction (inbox record and domain writes commit atomically)
  │
  └─► 6. Send ACK to RabbitMQ
```

---

## 3. Implementation in Code

In [tenant_provisioned_consumer.go:L64-L102](../notification-service/internal/consumer/tenant_provisioned_consumer.go#L64-L102) and [inbox_repository.go:L35-L68](../notification-service/internal/repository/inbox_repository.go#L35-L68):

```go
// ClaimEvent attempts an atomic insert into the inbox table
func (r *InboxRepository) ClaimEvent(ctx context.Context, tx *sql.Tx, msgID, consumer, evtType string) (bool, error) {
    query := `
        INSERT INTO public.inbox (message_id, consumer_name, event_type, processed_at)
        VALUES ($1, $2, $3, NOW())
        ON CONFLICT (message_id) DO NOTHING;
    `
    res, err := tx.ExecContext(ctx, query, msgID, consumer, evtType)
    if err != nil {
        return false, fmt.Errorf("failed to claim inbox event: %w", err)
    }

    rows, err := res.RowsAffected()
    if err != nil {
        return false, err
    }
    // rows == 1 means we claimed the event; rows == 0 means it was a duplicate
    return rows == 1, nil
}
```

If the consumer crashes midway through step 4:
- The database transaction rolls back, which undoes the inbox claim.
- The unacknowledged message returns to RabbitMQ.
- When redelivered, `ClaimEvent` succeeds again and re-attempts the work cleanly.

---

## 4. Message Acknowledgment Invariants

A critical design requirement is the order of database commit versus broker acknowledgment:

```go
// Correct: ACK only AFTER database transaction commits
err := txManager.WithTransaction(ctx, func(txCtx context.Context) error {
    claimed, err := inbox.Claim(txCtx, event.ID)
    if !claimed {
        return nil // Already processed
    }
    return domainService.Process(txCtx, event)
})
if err != nil {
    // Database or processing failed: NACK message to trigger retry
    msg.Nack(false, true)
    return
}
// Database is committed: safe to ACK
msg.Ack(false)
```

Never ACK a message before the database transaction has committed. A premature ACK combined with a subsequent database crash causes silent data loss.

---

## 5. Architectural Invariants & Operational Trade-offs

- **At-Least-Once Consumer Guard**: Downstream consumers assume duplicate delivery is an inherent characteristic of distributed networks, enforcing idempotency via natural keys or the inbox table.
- **Inbox Claim Atomicity**: The inbox claim and business mutations execute within the exact same database transaction, ensuring they commit or abort together.
- **Commit-Driven Acknowledgment**: Messages are acknowledged on the AMQP channel strictly after the local database transaction commits to disk.
