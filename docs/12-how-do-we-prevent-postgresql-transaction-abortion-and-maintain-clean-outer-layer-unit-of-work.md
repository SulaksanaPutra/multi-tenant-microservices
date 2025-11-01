# How Do I Prevent PostgreSQL Transaction Abortion and Maintain Clean Outer-Layer Unit-of-Work?

*Fixing Non-Atomic Inbox Guards, Uncovering PostgreSQL's Aborted Transaction Trap, and Restoring Clean Architecture*

---

## 1. The Initial Trap: "Just Put the Inbox Guard in the Service!"

When I set out to implement idempotent event processing in `user-service`, my goal was clear: if RabbitMQ delivers the exact same `workspace.initiated` event twice, my system must detect the duplicate `event_id` and skip execution cleanly without creating duplicate users or duplicate outbox events.

Naturally, my initial attempt was to add `inboxRepository` directly into `UserService` and execute the guard before creating the user:

```go
func (s *UserService) CreateUserFromWorkspace(ctx context.Context, input CreateUserFromWorkspaceInput) error {
    if input.EventID != "" && s.inboxRepository != nil {
        isDup, err := s.inboxRepository.TryInsert(ctx, input.EventID)
        if isDup {
            return nil // Skip duplicate
        }
    }
    // Create user...
    // Create outbox message...
}
```

In local happy-path tests with 100% network uptime, it looked like it worked. But when testing under network latency, transient database disconnects, and consumer retries, three catastrophic flaws surfaced.

---

## 2. Uncovering the Failure Modes

### Flaw #1: The Phantom Idempotency Trap (Silent Data Loss)

Because `inboxRepository.TryInsert`, `userRepository.CreateUser`, and `outboxRepository.CreateOutboxMessage` ran as independent, auto-committing queries outside of a unified transaction:

1. `inboxRepository.TryInsert` inserted `event_id="evt_123"` into PostgreSQL and committed immediately.
2. Next, `userRepository.CreateUser` failed due to a transient database connection timeout.
3. The message consumer caught the error and NACKed the message, causing RabbitMQ to retry.
4. On retry, `inboxRepository.TryInsert` saw `evt_123` already present in the inbox table, flagged `isDup = true`, and returned `nil` (Success)!
5. **The Result**: The event was ACKed and dropped by RabbitMQ. The user was **never created in the database**. The system suffered silent, permanent data loss.

### Flaw #2: The PostgreSQL Transaction Abortion Trap (`pq: 23505`)

My initial reaction to Flaw #1 was: *"Okay, let's just wrap `TryInsert`, `CreateUser`, and `CreateOutboxMessage` inside a single `sql.Tx`!"*

Inside `inbox_repository.go`, I was catching the unique constraint violation:
```go
const query = `INSERT INTO public.inbox (event_id) VALUES ($1);`
_, err := exec.ExecContext(ctx, query, eventID)
if err != nil {
    var pqErr *pq.Error
    if errors.As(err, &pqErr) && pqErr.Code == "23505" {
        return true, nil // isDuplicate = true
    }
}
```

This revealed a critical PostgreSQL engine behavior: **Unlike MySQL, PostgreSQL immediately invalidates the entire transaction block whenever any statement raises an exception (such as `23505`).**

Even though my Go code swallowed `pqErr.Code == "23505"` and returned `isDuplicate = true`, PostgreSQL internally marked the transaction state as `ERROR`. When `tx.Commit()` was called at the end of the block, PostgreSQL refused to commit and panicked with:
```text
pq: current transaction is aborted, commands ignored until end of transaction block
```

### Flaw #3: The Leaky Service Transaction Antipattern

Looking back at our engineering standards in [7-how-do-we-manage-database-transactions-and-domain-invariants-outer-layer-unit-of-work.md](./7-how-do-we-manage-database-transactions-and-domain-invariants-outer-layer-unit-of-work.md), injecting `inboxRepository` into `UserService` violated Clean Architecture. The inbox table is an infrastructure transport concern (RabbitMQ message deduplication). Forcing `UserService` to manage transport inbox tables coupled domain logic to messaging details.

---

## 3. The Resolution: Outer-Layer Consumer Unit-of-Work + `ON CONFLICT DO NOTHING`

To solve all three issues cleanly, I restructured the system around two key principles:

1. **Move Transaction & Inbox Guard to the Consumer Layer**: The consumer (`WorkspaceInitiatedConsumer`) opens the transaction, checks `InboxRepository`, calls `UserService` (which handles pure domain state + outbox), and commits.
2. **Use PostgreSQL `ON CONFLICT DO NOTHING`**: Instead of triggering `23505` constraint errors, the SQL statement uses `ON CONFLICT (event_id) DO NOTHING` and checks `RowsAffected() == 0`. This keeps the PostgreSQL transaction state completely healthy.

---

## 4. The Refactored Code in Action

### Step 1: Non-Aborting Inbox Repository ([inbox_repository.go](../user-service/internal/repository/inbox_repository.go))

I updated `inbox_repository.go` to use `ON CONFLICT DO NOTHING`:

```go
func (r *InboxRepository) TryInsert(ctx context.Context, eventID string) (bool, error) {
    exec := txcontext.GetExecutor(ctx, r.client.DB)
    const query = `INSERT INTO public.inbox (event_id) VALUES ($1) ON CONFLICT (event_id) DO NOTHING;`
    res, err := exec.ExecContext(ctx, query, eventID)
    if err != nil {
        return false, fmt.Errorf("failed to insert event_id into inbox: %w", err)
    }
    rows, err := res.RowsAffected()
    if err != nil {
        return false, fmt.Errorf("failed to check rows affected in inbox insert: %w", err)
    }
    if rows == 0 {
        return true, nil // 0 rows affected => Duplicate!
    }
    return false, nil // Successfully inserted
}
```

### Step 2: Pure Domain Service ([user_service.go](../user-service/internal/service/user_service.go))

I removed `inboxRepository` completely from `UserService`. `UserService` now focuses purely on business logic (`User`) and transactional outbox events (`OutboxMessage`):

```go
type UserService struct {
    userRepository   UserRepository
	outboxRepository OutboxRepository
}

func (s *UserService) CreateUserFromWorkspace(ctx context.Context, input CreateUserFromWorkspaceInput) error {
    userID := domain.GenerateUserID()
    userObj := domain.User{
        ID:    userID,
        Email: input.OwnerEmail,
        Name:  input.OwnerName,
    }

    if err := s.userRepository.CreateUser(ctx, userObj); err != nil {
        return fmt.Errorf("failed to create user: %w", err)
    }

    // Write Outbox message in the same propagated transaction context...
}
```

### Step 3: Consumer Outer-Layer Orchestration ([workspace_initiated_consumer.go](../user-service/internal/consumer/workspace_initiated_consumer.go))

I injected `inboxRepository` into `WorkspaceInitiatedConsumer` and wrapped both `inboxRepository.TryInsert` and `userService.CreateUserFromWorkspace` in `c.txManager.WithTransaction`:

```go
var isDuplicate bool
err := c.txManager.WithTransaction(ctx, func(txCtx context.Context) error {
    if evt.EventID != "" && c.inboxRepository != nil {
        isDup, err := c.inboxRepository.TryInsert(txCtx, evt.EventID)
        if err != nil {
            return fmt.Errorf("inbox guard failed: %w", err)
        }
        if isDup {
            isDuplicate = true
            log.Printf("WorkspaceInitiatedConsumer: Duplicate event_id='%s' detected by Inbox guard. Skipping processing.", evt.EventID)
            return nil
        }
    }

    return c.userService.CreateUserFromWorkspace(txCtx, input)
})

if err != nil {
    log.Printf("Error processing WorkspaceInitiated event: %v", err)
    _ = d.Nack(false, true)
    continue
}

if isDuplicate {
    log.Printf("WorkspaceInitiatedConsumer: Gracefully ACKing duplicate event_id='%s'.", evt.EventID)
}

_ = d.Ack(false)
```

---

## 5. Architectural Summary & Verification

| Dimension | Before Refactor | After Outer-Layer Refactor |
| :--- | :--- | :--- |
| **Transaction Ownership** | Disjoint / Auto-committing queries | **Outer Consumer (`txManager.WithTransaction`)** |
| **Postgres Error Safety** | Swallowed `23505` (Aborted `sql.Tx`) | **`ON CONFLICT DO NOTHING` (`RowsAffected() == 0`)** |
| **Clean Architecture** | `inboxRepository` inside `UserService` | **Pure `UserService` (Domain + Outbox only)** |
| **Transient Error Retry** | Risk of silent user loss on retry | **Atomic Rollback (Inbox + User + Outbox)** |

By enforcing the **Outer-Layer Unit-of-Work** at the consumer boundary and leveraging PostgreSQL's `ON CONFLICT DO NOTHING`, I eliminated silent data loss, protected transaction state, and kept the domain layer pristine.
