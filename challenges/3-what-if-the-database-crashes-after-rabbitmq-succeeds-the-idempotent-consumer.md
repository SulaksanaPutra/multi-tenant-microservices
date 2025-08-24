# What If the Database Crashes After RabbitMQ Succeeds? The Idempotent Consumer.

*Surviving Hardware Failures, Power-Outage Scenarios, and Building Crash-Proof Consumers with the Inbox Pattern*

---

## 1. The New Mystery: "I Fixed the Race Condition, But Duplicates Are Still Happening"

After deploying the atomic CTE batch-claiming fix from [Challenge #2](./2-how-does-the-phantom-batch-duplicate-delivery-happen-and-how-do-we-fix-it.md), I felt safe again.

`FOR UPDATE SKIP LOCKED` eliminates duplicate delivery when two healthy workers race each other on a healthy database. But a new bug report arrived from the on-call rotation at 3 AM:

> **"We see occasional duplicate welcome emails. Only one every few hours, but it's there. The database logs show zero concurrent worker conflicts. What is still causing this?"**

The database was healthy. There was only **one** instance of `user-service` running at the time. So what happened?

---

## 2. The Real Enemy: The Millisecond Gap After Publish

The fix in Challenge #2 solved the **concurrency problem** (two workers racing on a healthy system). But it left a second, darker failure mode completely untouched: **the physical crash scenario**.

Here is the exact timeline of what went wrong:

```
Timeline          user-service Outbox Worker (Single Instance)
─────────────────────────────────────────────────────────────────────
  T = 0ms    Runs CTE Query.
             PostgreSQL atomically locks Row #1 and marks it PROCESSING.

  T = 10ms   Publishes Row #1 to RabbitMQ.
             RabbitMQ says: "Got it! ✓"
             ▲
             └─ The message has LEFT the building.

  T = 11ms   Worker turns to PostgreSQL to run:
             UPDATE outbox SET status = 'PUBLISHED' WHERE id = 1;
             ▲
             └─ 💥 THE SERVER RUNS OUT OF RAM. THE OS KILLS THE PROCESS.
                (Or: the network cable is cut. Or: Postgres restarts.)

  T = 30s    The server reboots.
             The RecoverStuckClaims sweeper runs.
             It finds Row #1 still in PROCESSING (30s timeout expired).
             It resets Row #1 back to PENDING.

  T = 30s+   The worker wakes up and publishes Row #1 AGAIN.
             A duplicate user.created event is now in RabbitMQ.
```

### Why No SQL Can Save You Here

The crash happened in the tiny window **after the message was published to RabbitMQ but before PostgreSQL could record that fact**. The outbox's job is to guarantee delivery. It did its job — it will keep retrying until the message is confirmed. But the broker already has the message and will deliver it twice.

There is no SQL query in the world that can fix this. The broken moment is between two completely separate systems. **The fix must happen on the Consumer Side.**

---

## 3. The Multi-Consumer Problem: Different Services, Different Fixes

Looking at the downstream consumers on the `company.events` topic exchange:

```
user-service  ──► company.events ──► auth-service          (copies user_tenant_memberships)
                              └──► notification-service  (sends welcome email)
                              └──► tenant-service        (records service infrastructure)

tenant-service  ──► company.events ──► infra-provisioner  (provisions containers/databases)
                              └──► notification-service  (waits for workspace.ready barrier)
```

Every consumer must be made **Idempotent**: processing the same message twice must produce the same result as processing it once.

But these services have very different business logic, so they need different approaches.

---

## 4. The Easy Way: Natural Database Constraints

### 4a. auth-service — `user_tenant_memberships` Copy (`user.created`)

`auth-service` receives `user.created` and mirrors a `(user_id, tenant_id)` row into its local `user_tenant_memberships` table.

If it receives the exact same `user.created` event twice, what happens?

**Before the fix:** The second call to add the membership would violate the primary key and throw an error. The consumer would NACK, and RabbitMQ would requeue it — causing an infinite retry loop.

**After the fix:** We rely on PostgreSQL's `ON CONFLICT` to make the insert a no-op on duplicate calls. You can see this live in [credential_repository.go](../auth-service/internal/repository/credential_repository.go):

```go
func (r *CredentialRepository) AddMembership(ctx context.Context, userID, tenantID string) error {
    exec := txcontext.GetExecutor(ctx, r.dbClient)
    query := `
        INSERT INTO public.user_tenant_memberships (user_id, tenant_id)
        VALUES ($1, $2)
        ON CONFLICT (user_id, tenant_id) DO NOTHING;
    `
    if _, err := exec.ExecContext(ctx, query, userID, tenantID); err != nil {
        return fmt.Errorf("credential repository: failed to add tenant membership ...")
    }
    return nil
}
```

When the duplicate `user.created` message arrives, `auth-service` runs the same insert. PostgreSQL says *"I already did this"* and returns success without changing anything. The consumer ACKs the duplicate and nobody gets hurt.

### 4b. tenant-service — `tenant_infrastructures` Routing Write-Back (`tenant.order_db.ready`)

`tenant-service` receives `tenant.order_db.ready` events from the `infra-provisioner` and records the sanitized routing metadata (host, port, db name, schema) in its control-plane registry table.

If the same event is delivered twice, the upsert must be a no-op. See [tenant_infrastructure_repository.go](../tenant-service/internal/repository/tenant_infrastructure_repository.go):

```go
INSERT INTO public.tenant_infrastructures
    (tenant_id, service_name, db_host, db_port, db_name, db_user, schema_name)
VALUES ($1, $2, $3, $4, $5, $6, $7)
ON CONFLICT (tenant_id, service_name) DO UPDATE
    SET db_host = EXCLUDED.db_host, ...;
```

Both examples share one principle: **if the consumer's business logic naturally maps to idempotent SQL operations, use natural constraints.**

---

## 5. The Bulletproof Way: The Inbox Pattern (notification-service)

Natural constraints are not always available. Consider `notification-service`.

If it receives `user.created` and `workspace.ready` twice, it will call the SMTP server twice. **The user gets two welcome emails.** There is no `ON CONFLICT` clause for sending an email — an email is not a database row you can deduplicate.

To fix this, we build the exact inverse of the Outbox Pattern: **The Inbox Pattern** (also called a Deduplication Table).

### The Core Insight: Every Message Has a Unique ID

Every outbox row has a unique `id` (e.g. `outbox_abc123xyz`). We embed this as `event_id` in the published event payload:

```go
// In user-service publisher
type UserCreatedEvent struct {
    EventID   string    `json:"event_id"`   // ← The outbox row's ID, now carried with the message
    UserID    string    `json:"user_id"`
    TenantID  string    `json:"tenant_id"`
    Email     string    `json:"email"`
    Name      string    `json:"name"`
    CreatedAt time.Time `json:"created_at"`
}
```

The first time this message is published, `event_id` is `outbox_abc123`. If it gets published a second time (because the DB crashed before `MarkPublished` ran), it carries the exact same `event_id: outbox_abc123`. This ID is our duplicate detector.

### The Inbox Table

In each consuming service's database, a simple deduplication table acts as the memory:

```sql
CREATE TABLE IF NOT EXISTS public.inbox (
    event_id     VARCHAR(255) PRIMARY KEY,
    tenant_id    VARCHAR(255),
    event_type   VARCHAR(255),
    payload      JSONB,
    processed_at TIMESTAMPTZ DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_inbox_tenant_event ON public.inbox(tenant_id, event_type);
```

Its `PRIMARY KEY` on `event_id` makes it physically impossible to insert the same event twice.

### The Flow: Guarding with a DB Transaction

You can see this implemented in [inbox_repository.go](../notification-service/internal/repository/inbox_repository.go) (identical in [auth-service](../auth-service/internal/repository/inbox_repository.go) and [tenant-service](../tenant-service/internal/repository/inbox_repository.go)):

```go
func (r *InboxRepository) TryInsert(ctx context.Context, input CreateInboxMessageInput) (bool, error) {
    exec := txcontext.GetExecutor(ctx, r.dbClient)
    const query = `
        INSERT INTO public.inbox (event_id, tenant_id, event_type, payload)
        VALUES ($1, $2, $3, $4)
        ON CONFLICT (event_id) DO NOTHING;
    `
    res, err := exec.ExecContext(ctx, query, input.EventID, input.TenantID, input.EventType, string(input.Payload))
    if err != nil {
        return false, fmt.Errorf("failed to insert inbox record: %w", err)
    }
    rows, err := res.RowsAffected()
    if err != nil {
        return false, fmt.Errorf("failed to check rows affected in inbox insert: %w", err)
    }
    if rows == 0 {
        return true, nil // isDuplicate = true
    }
    return false, nil // isDuplicate = false, safe to process
}
```

And the consumer's phase-1 guard in [user_created_consumer.go](../notification-service/internal/consumer/user_created_consumer.go):

```go
err := c.txManager.WithTransaction(ctx, func(txCtx context.Context) error {
    inboxInput := repository.CreateInboxMessageInput{
        EventID:   evt.EventID,
        TenantID:  evt.TenantID,
        EventType: domain.RoutingKeyUserCreated,
        Payload:   d.Body,
    }

    // Step 1: transactional inbox guard — deduplicates the event.
    isDup, err := c.inboxService.ClaimEvent(txCtx, inboxInput)
    if err != nil {
        return fmt.Errorf("inbox guard failed: %w", err)
    }
    if isDup {
        log.Printf("UserCreatedConsumer: Duplicate event_id='%s' detected. Skipping.", evt.EventID)
        return nil
    }

    // ... evaluate the barrier, persist pending audit log inside TX, commit ...
})

// Phase 2 (post-commit): dispatch email. SMTP never holds DB locks.
```

### The Barrier: notification-service Needs Two Events

`notification-service` must not send the welcome email until the workspace is actually ready. It waits for **both** `user.created` **and** `workspace.ready` for the same tenant. The inbox table is also used as the barrier read — see `ProcessEventAndTrySendWelcome` in [notification_service.go](../notification-service/internal/service/notification_service.go):

```go
func (s *NotificationService) ProcessEventAndTrySendWelcome(ctx context.Context, input ProcessEventInput, events []domain.InboxMessage) (*ProcessEventOutput, error) {
    // events = c.inboxService.GetBarrierEvents(txCtx, tenantID)
    var hasUserCreated, hasWorkspaceReady bool
    for _, evt := range events {
        if evt.EventType == "user.created" {
            hasUserCreated = true
        } else if evt.EventType == "workspace.ready" {
            hasWorkspaceReady = true
        }
    }
    if !hasUserCreated || !hasWorkspaceReady {
        log.Printf("NotificationService: Barrier condition not met yet (user_created=%v, workspace_ready=%v). Waiting...", ...)
        return nil, nil
    }
    // ... persist pending audit log inside TX ...
}
```

### Why This Survives Every Crash Scenario

```
Scenario A: notification-service crashes BEFORE sending email
─────────────────────────────────────────────────────────────
  1. inbox INSERT succeeds, TX commits
  2. 💥 CRASH before email is sent, before ACK
  3. RabbitMQ re-queues the message (no ACK received)
  4. Service restarts, processes the message again
  5. inbox INSERT → unique violation → isDuplicate = true
  6. ACK sent, message deleted
  7. 😢 User misses the welcome email (email was never sent)
     BUT: No duplicate email. No data corruption.
  Acceptable: one lost email vs. infinite duplicates.

Scenario B: notification-service crashes AFTER sending email, BEFORE ACK
─────────────────────────────────────────────────────────────────────────
  1. inbox INSERT succeeds, TX commits, email is sent
  2. 💥 CRASH before ACK
  3. RabbitMQ re-queues the message
  4. Service restarts, processes the message again
  5. inbox INSERT → unique violation → isDuplicate = true
  6. ACK sent, message deleted
  7. User got exactly one email. No duplicate.
```

The `inbox` table acts as a permanent, crash-safe record of what has been processed. Once `event_id` is committed to the inbox table, it will block any future duplicate forever, even across server restarts.

---

## 6. The Full System: Combining Outbox + Inbox

By combining both patterns, the entire event pipeline becomes resilient to any failure at any point:

```
┌───────────────────────────────────────────────────────────────────────────────┐
│                             SENDER SIDE (user-service)                        │
│                                                                               │
│  [HTTP Request] ──► [DB Transaction] ──► [users + outbox rows COMMITTED]     │
│                                                   │                           │
│                                          [OutboxWorker polls]                 │
│                                                   │                           │
│                                    [CTE: PENDING → PROCESSING]                │
│                                                   │                           │
│                                    [Publish to RabbitMQ ✓]                   │
│                                                   │                           │
│                                    [PROCESSING → PUBLISHED]                  │
│                                                   │                           │
│                          If DB crashes here ──────┘                           │
│                          RecoverStuckClaims resets to PENDING                 │
│                          Message is re-published (DUPLICATE SENT!)            │
└───────────────────────────────────────────────────────────────────────────────┘
                                       │
                                  company.events
                                       │
              ┌────────────────────────┴────────────────────────┐
              ▼                                                 ▼
┌─────────────────────────────┐              ┌─────────────────────────────────┐
│  CONSUMERS: auth-service    │              │ CONSUMER: notification-service  │
│  tenant-service             │              │ (Inbox Pattern + Barrier)       │
│  (Easy Way)                 │              │                                 │
│                             │              │  INSERT INTO inbox (event_id)   │
│  ON CONFLICT DO NOTHING /   │              │  ├─ duplicate? → ACK, skip      │
│  DO UPDATE                  │              │  └─ OK?    → read barrier,      │
│                             │              │              process + commit  │
│  Naturally Idempotent       │              │  Bulletproof Deduplication      │
└─────────────────────────────┘              └─────────────────────────────────┘
```

Network cables can be cut, databases can reboot, and pods can be OOM-killed. Your data will always remain perfectly consistent.

---

## 7. Summary: The Two-Layer Defense

| Layer | Pattern | Where | Mechanism |
|-------|---------|--------|-----------|
| **Sender** | Transactional Outbox | user-service / tenant-service | Atomically stages events in DB; retries on crash |
| **Sender (concurrency)** | CTE + `FOR UPDATE SKIP LOCKED` | user-service / tenant-service | Prevents dual-worker phantom batch |
| **Consumer (easy)** | Natural DB Constraints | auth-service / tenant-service | `ON CONFLICT (..) DO NOTHING` / `DO UPDATE` |
| **Consumer (bulletproof)** | Inbox Pattern | notification-service | Deduplication table blocks repeated `event_id` |

**The rule of thumb**: If your consumer's business logic naturally maps to idempotent SQL operations, use natural constraints. If it triggers side effects that can't be undone (emails, HTTP calls, financial transactions), always use the Inbox Pattern.
