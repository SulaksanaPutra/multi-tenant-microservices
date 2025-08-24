# What Happens If the Event Broadcaster Breaks, and How Do I Safely Retry?

*Solving Distributed Dual-Write Failures with the Transactional Outbox Pattern*

---

## 1. The Naive Dream: "Just Publish After Saving to the DB!"

Imagine building my user registration system. A new user signs up via `POST /api/v1/register`.
I save the user into PostgreSQL:
```go
db.Exec("INSERT INTO users (id, email, name) VALUES ...")
```
And right after that line of code, I publish a `USER_REGISTERED` event to RabbitMQ so the notification service can send a welcome email and the tenant provisioner can set up workspace schema:
```go
rabbitmq.Publish("user.events", eventPayload)
```

It looks simple, clean, and elegant. In local testing with a single user and 100% network uptime, it works perfectly.

---

## 2. The Dreaded Realization: "Wait... What If the Broadcaster Breaks Right Here?"

One day, while reviewing production readiness, a critical question comes up:

> **"What happens if my process crashes, or RabbitMQ network drops RIGHT AFTER the user is saved to PostgreSQL, but BEFORE `rabbitmq.Publish()` executes?"**

I traced the code execution and realized the terrifying reality:

1. **PostgreSQL transaction committed**: The user row exists in the database. The client got a `201 Created` HTTP response.
2. **RabbitMQ call fails**: The app crashed or the broker network blipped.
3. **The Result**: **A Silent Data Discrepancy.** The user is registered, but no email is sent, no tenant schema is provisioned, and no error is thrown to the user. The event vanished into thin air.

Then I asked the inverse question:
> **"What if I publish to RabbitMQ FIRST, and then save to PostgreSQL?"**

If `rabbitmq.Publish()` succeeds, but the PostgreSQL `INSERT` fails due to a unique email constraint violation or database timeout...
**A Phantom Event is Born.** The notification service receives an event and sends a welcome email to a user that *does not exist in my database*.

Neither approach is safe. This is the classic **Dual-Write Problem** in distributed systems.

---

## 3. The Turning Point: The Transactional Outbox Pattern in Code

To prevent both lost events and phantom events, I needed a single source of truth that guarantees **Atomic Consistency**.

Instead of making a network call to RabbitMQ during the HTTP request path, I stage the event directly inside PostgreSQL within the **exact same database transaction (`sql.Tx`)** as the user registration!

You can see this implemented directly in [user_service.go:L65-122](../user-service/internal/service/user_service.go#L65-L122):

```go
// 2. Database Transaction Management
tx, err := s.dbClient.BeginTx(ctx, nil)
if err != nil {
    return nil, fmt.Errorf("failed to start database transaction: %w", err)
}
defer tx.Rollback()

// 3. Persist User Record
if err := s.userRepository.CreateUser(ctx, tx, userObj); err != nil {
    return nil, err
}

// 4. Persist Tenant Record
if err := s.tenantRepository.CreateTenant(ctx, tx, tenantObj); err != nil {
    return nil, err
}

// 5. Stage Domain Event Payload inside Transactional Outbox
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

// Commit atomically: User, Tenant, AND Outbox Event succeed together!
if err := tx.Commit(); err != nil {
    return nil, fmt.Errorf("failed to commit transaction: %w", err)
}
```

Now, the HTTP request finishes in milliseconds. The event is safely written to disk in PostgreSQL in `PENDING` status. If PostgreSQL rolls back, **neither** the user nor the outbox message is created. If it commits, the event is **guaranteed to be persisted**.

---

## 4. Anatomy of the Outbox Table: Why Every Column Matters

When defining the `public.outbox` table in [init.sql:L40-L54](../broker/init.sql#L40-L54), every single column was added to solve a specific production challenge:

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

### The Story Behind the Schema Design:
* **`payload` (`JSONB`)**: Why store the entire JSON payload instead of querying the `users` table later? Because domain entities change over time! Storing an immutable snapshot at creation time ensures consumers receive the exact data as it existed when the event happened.
* **`claimed_at` (`TIMESTAMP`)**: Added to detect **Worker Crashes**. When a worker picks up a batch of events, it sets `claimed_at = NOW()` and `status = 'PROCESSING'`.
* **`next_retry_at` (`TIMESTAMP`)**: Added to support **Exponential Backoff**. If publishing fails, I don't want to retry instantly in an infinite loop. I schedule the next retry into the future.
* **`last_error` (`VARCHAR(500)`)**: Captures sanitized diagnostic traces so I can inspect *why* a broadcast failed without digging through raw logs.

### The Secret Performance Saver: The Partial Index
Over months of running in production, millions of events will be processed (`status = 'PUBLISHED'`). If I created a standard index on `status`, the index would bloat to gigabytes, slowing down every read and write.

So I created a **Partial Index** in [init.sql:L62-L63](../broker/init.sql#L62-L63):

```sql
CREATE INDEX IF NOT EXISTS idx_outbox_pending ON public.outbox(event_type, next_retry_at, created_at)
    WHERE status IN ('PENDING', 'PROCESSING');
```
Because `PUBLISHED` rows are completely excluded, this index stays tiny (only indexing active pending rows). Searching for pending outbox events takes **less than 1ms**, no matter how many millions of historic events exist in the table!

---

## 5. The Broadcaster's Journey: Processing, Scaling, & Surviving Disasters

Now comes the worker (the broadcaster background process). How does it handle real-world operational challenges in code?

### Story 1: What happens when the Worker process crashes mid-delivery?
Imagine Worker Instance A claims 50 pending events, updates their status to `PROCESSING`, and starts publishing them to RabbitMQ one by one. Suddenly, at message #10, the server loses power or gets killed by a Kubernetes OOM-killer.

Messages #10 to #50 are left hanging with status `PROCESSING`.

How do I recover? Every 5 seconds, the fallback ticker in [outbox_worker.go:L69-72](../user-service/internal/worker/outbox_worker.go#L69-L72) calls `RecoverStuckClaims`, implemented in [outbox_repository.go:L157-L171](../user-service/internal/repository/outbox_repository.go#L157-L171):

```go
func (r *postgresOutboxRepository) RecoverStuckClaims(ctx context.Context, eventType string) error {
	const query = `
		UPDATE public.outbox
		SET status     = 'PENDING',
		    claimed_at = NULL
		WHERE status     = 'PROCESSING'
		  AND event_type = $1
		  AND claimed_at < NOW() - $2::interval;
	`
	_, err := r.db.ExecContext(ctx, query, eventType, fmt.Sprintf("%d seconds", int(stuckClaimTimeout.Seconds())))
	return err
}
```
Any message stuck in `PROCESSING` for more than 30 seconds (`stuckClaimTimeout = 30s`) is recognized as abandoned by a dead worker. It gets reset to `PENDING`, and another healthy worker instance picks it up seamlessly. **Zero lost events!**

---

### Story 2: What happens when RabbitMQ goes down? Retries & Exponential Backoff
Suppose RabbitMQ loses network connectivity for 1 minute.

When the worker attempts to publish an outbox event in [outbox_worker.go:L129-L131](../user-service/internal/worker/outbox_worker.go#L129-L131), RabbitMQ returns an error. Instead of discarding the message or crashing the application, the repository triggers `MarkFailed`, shown in [outbox_repository.go:L189-L211](../user-service/internal/repository/outbox_repository.go#L189-L211):

```go
func (r *postgresOutboxRepository) MarkFailed(ctx context.Context, id string, err error) error {
	safeErr := sanitizeError(err)
	const query = `
		UPDATE public.outbox
		SET retry_count   = retry_count + 1,
		    last_error    = $2,
		    claimed_at    = NULL,
		    next_retry_at = CASE
		        WHEN retry_count + 1 < $3
		        THEN NOW() + (INTERVAL '1 second' * POWER(2, retry_count + 1))
		        ELSE NULL
		    END,
		    status = CASE
		        WHEN retry_count + 1 >= $3 THEN 'FAILED'
		        ELSE 'PENDING'
		    END
		WHERE id = $1;
	`
	_, dbErr := r.db.ExecContext(ctx, query, id, safeErr, maxRetries)
	return dbErr
}
```

Here is how the retry timeline unfolds:
* **Failure #1**: Retried after $2^1 = 2$ seconds.
* **Failure #2**: Retried after $2^2 = 4$ seconds.
* **Failure #3**: Retried after $2^3 = 8$ seconds.
* **Failure #4**: Retried after $2^4 = 16$ seconds.
* **Failure #5**: `retry_count` reaches 5 $\rightarrow$ Status changes to `FAILED`.

By spacing out retries exponentially, I give RabbitMQ time to recover without overwhelming the broker. And if a message permanently fails (e.g. invalid message format), it stops retrying after 5 attempts and lands in `FAILED` status for manual investigation.

*Bonus Security*: Notice `safeErr := sanitizeError(err)` in [outbox_repository.go:L27-L37](../user-service/internal/repository/outbox_repository.go#L27-L37)! It scrubs database passwords and AMQP connection URLs using regex before persisting them to disk.

---

### Story 3: Running Multiple Instances Without Duplicate Deliveries
When traffic grows and I deploy 5 replicas of `user-service`, how do I stop Instance #1 and Instance #2 from claiming and sending the exact same outbox message at the same time?

I use PostgreSQL's atomic Common Table Expression (CTE) with **`FOR UPDATE SKIP LOCKED`** in [outbox_repository.go:L111-L133](../user-service/internal/repository/outbox_repository.go#L111-L133):

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

`FOR UPDATE SKIP LOCKED` tells PostgreSQL: *"If Instance #1 is currently locking Row X, Instance #2 should immediately skip Row X without waiting and grab Row Y instead."*

This allows all 5 worker instances to process events concurrently at maximum speed, with zero row contention and zero duplicate deliveries!

---

### Story 4: Blazing Fast Speed with Hybrid Poke & Debouncing
Polling the database every 5 seconds means a user might wait 5 seconds before their welcome email is sent. But querying PostgreSQL every 1 millisecond would burn CPU.

I solved this with a **Hybrid Poke & Debounce Architecture** in [outbox_worker.go:L48-L95](../user-service/internal/worker/outbox_worker.go#L48-L95):

1. **Instant Notification (`Poke()`)**: The moment `RegisterUser` commits, it calls `s.outboxWorker.Poke()` ([user_service.go:L127](../user-service/internal/service/user_service.go#L127)), sending a non-blocking signal down a Go channel:
   ```go
   func (w *OutboxWorker) Poke() {
       select {
       case w.wakeUpChan <- struct{}{}:
       default:
       }
   }
   ```
2. **10ms Micro-Debouncing**: If 50 users sign up within the same 5 milliseconds, 50 `Poke()` signals arrive. Instead of executing 50 separate database queries, `debounceAndProcess` ([outbox_worker.go:L78-L95](../user-service/internal/worker/outbox_worker.go#L78-L95)) waits 10 milliseconds, collects all 50 pokes, and processes them all in a single batch query!
3. **5-Second Fallback Sweeper**: If a wake-up signal was somehow dropped, the 5-second ticker acts as a backup net to catch any remaining pending events.