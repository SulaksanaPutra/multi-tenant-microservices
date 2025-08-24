# How Does Duplicate Delivery Happen After Implementing the Outbox Pattern, and How Do I Fix It?

*Solving Race Conditions, The Millisecond Gap, and Atomic CTE Batch Claiming*

---

## 1. The Post-Outbox Mystery: "I Implemented the Outbox... So Why Am I Seeing Duplicate Events?"

After implementing the Transactional Outbox Pattern, I felt invincible. I eliminated lost events, survived broker outages, and guaranteed that user registrations in PostgreSQL and outbox records in PostgreSQL were committed atomically together.

Then, I scaled up production traffic and deployed 2 replicas of `user-service`.

A few hours later, a mysterious bug report arrived:

> **"Users are receiving duplicate welcome emails, and some tenants are getting provisioned twice! I checked PostgreSQL logs—the database is 100% healthy, zero connection timeouts, zero failed transactions. How is my brand-new Outbox Pattern emitting duplicate event broadcasts?"**

I sat down to debug. The database hadn't broken. So why were duplicate messages being published to RabbitMQ?

---

## 2. The Naive Outbox Worker: How I First Processed Outbox Messages

To understand how duplicate delivery happens after implementing the Outbox Pattern, look at how my outbox background worker originally fetched and processed staged events.

On paper, the worker loop seemed completely logical:

```
┌────────────────────────────────────────────────────────────┐
│ 1. SELECT * FROM outbox WHERE status = 'PENDING' LIMIT 50  │
└─────────────────────────────┬──────────────────────────────┘
                              │
                              ▼
┌────────────────────────────────────────────────────────────┐
│ 2. Iterate through batch & publish events to RabbitMQ      │
└─────────────────────────────┬──────────────────────────────┘
                              │
                              ▼
┌────────────────────────────────────────────────────────────┐
│ 3. UPDATE outbox SET status = 'PUBLISHED' WHERE id = ...   │
└────────────────────────────────────────────────────────────┘
```

When running locally with a single service instance, this 3-step loop worked flawlessly. But as soon as I scaled out to multiple service instances (Worker A and Worker B), a race condition known as **The Phantom Batch** was triggered.

---

## 3. The Millisecond Gap: Timeline of a Duplicate Fire

Here is the exact step-by-step timeline of what happened when Worker A and Worker B ran concurrently on a 100% healthy database:

```
Timeline (ms)    Worker A (Instance 1)                    Worker B (Instance 2)
  T = 0ms   ────► Runs SELECT (Gets Row #1, PENDING)
  T = 10ms  ─────────────────────────────────────────────► Runs SELECT (Gets Row #1, PENDING!)
                                                           ▲
                                                           └─ The Trap: Worker A hasn't run UPDATE yet!
  T = 20ms  ────► Publishes Message #1 to RabbitMQ
  T = 25ms  ─────────────────────────────────────────────► Publishes Message #1 to RabbitMQ (DUPLICATE!)
  T = 30ms  ────► Runs UPDATE (Row #1 -> PUBLISHED)
  T = 35ms  ─────────────────────────────────────────────► Runs UPDATE (Row #1 -> PUBLISHED)
```

### Dissecting the Race Condition:
1. **T = 0ms**: Worker A runs `SELECT * FROM outbox WHERE status = 'PENDING'`. PostgreSQL returns Row #1 (`status = 'PENDING'`).
2. **T = 10ms**: Worker B wakes up (triggered by a ticker or `Poke()`) and runs the exact same `SELECT` query.
3. **The Trap**: Worker A is busy formatting JSON or opening a TCP socket to RabbitMQ. It **has not updated Row #1 yet**. When PostgreSQL receives Worker B's query, it checks Row #1, sees its status is still `PENDING`, and happily returns Row #1 to Worker B as well!
4. **T = 20ms**: Worker A publishes Message #1 to RabbitMQ.
5. **T = 25ms**: Worker B publishes Message #1 to RabbitMQ. **A Duplicate Message Is Emitted!** Downstream consumers receive two identical events.
6. **T = 30ms & 35ms**: Both workers run `UPDATE outbox SET status = 'PUBLISHED'`. Worker B's update simply overwrites Worker A's update.

**PostgreSQL did not break.** It executed every SQL query cleanly. The duplicate happened because Worker B sneaked into the **30-millisecond gap** between Worker A's `SELECT` and `UPDATE`.

---

## 4. The Fix: Atomic CTE Batch Claiming (`FOR UPDATE SKIP LOCKED`)

To stop healthy servers from tripping over each other, I had to eliminate the time gap between reading and updating.

Instead of performing a `SELECT` followed by a later `UPDATE`, I combined both operations into **a single Atomic Operation** using a PostgreSQL Common Table Expression (CTE) with `FOR UPDATE SKIP LOCKED`.

You can see this implemented in my codebase inside [outbox_repository.go:L111-L133](../user-service/internal/repository/outbox_repository.go#L111-L133):

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

## 5. The Fixed Timeline: Zero Duplicate Messages

Now, let's watch how the exact same multi-worker scenario behaves with the CTE query:

```
Timeline (ms)    Worker A (Instance 1)                    Worker B (Instance 2)
  T = 0ms   ────► Runs CTE Query
                  (Postgres locks Row #1 & 
                   updates status -> PROCESSING)
  T = 10ms  ─────────────────────────────────────────────► Runs CTE Query
                                                           (Postgres sees Row #1 is LOCKED,
                                                            SKIPS IT, locks & returns Row #2!)
  T = 20ms  ────► Publishes Message #1 to RabbitMQ        
  T = 25ms  ─────────────────────────────────────────────► Publishes Message #2 to RabbitMQ (NO DUPLICATE!)
  T = 30ms  ────► Runs MarkPublished (Row #1 -> PUBLISHED)
  T = 35ms  ─────────────────────────────────────────────► Runs MarkPublished (Row #2 -> PUBLISHED)
```

### Why This Fix Works Flawlessly:
1. **Single Microsecond Mutation**: Selecting rows and transitioning status `PENDING` $\rightarrow$ `PROCESSING` happens in **one single atomic PostgreSQL operation**. There is zero time gap for another worker query to sneak in.
2. **`FOR UPDATE SKIP LOCKED`**: Tells PostgreSQL: *"If Worker A is currently inspecting or locking Row #1, Worker B must skip Row #1 immediately without waiting, and claim Row #2 instead."*
3. **Status Isolation**: Once a row becomes `PROCESSING`, any subsequent worker query filtering on `status = 'PENDING'` will ignore it.

---

## 6. Code Walkthrough in My Repository

In my codebase ([outbox_worker.go:L109-L137](../user-service/internal/worker/outbox_worker.go#L109-L137)), the worker process calls `FetchAndClaimBatch` first before attempting any network publishing:

```go
func (w *OutboxWorker) processBatch(ctx context.Context) {
    // 1. Atomically claim batch (PENDING -> PROCESSING via FOR UPDATE SKIP LOCKED)
    messages, err := w.outboxRepository.FetchAndClaimBatch(ctx, w.eventType, w.batchSize)
    if err != nil || len(messages) == 0 {
        return
    }

    // 2. Publish each claimed message safely (guaranteed unique ownership)
    for _, msg := range messages {
        var evt publisher.UserRegisteredEvent
        _ = json.Unmarshal(msg.Payload, &evt)

        if pubErr := w.publisher.PublishUserRegistered(ctx, evt); pubErr != nil {
            _ = w.outboxRepository.MarkFailed(ctx, msg.ID, pubErr)
        } else {
            // 3. Transition status from PROCESSING to PUBLISHED
            _ = w.outboxRepository.MarkPublished(ctx, msg.ID)
        }
    }
}
```
