# PostgreSQL Transaction Invalidation and Clean Outer Unit-of-Work

*Avoiding aborted transaction cascades (`pq: 23505`), atomic inbox processing, and layer boundaries.*

---

## 1. Antipattern: Transport Guards Inside Domain Services

When implementing event deduplication in `user-service`, a common mistake is placing the inbox repository check directly inside domain business logic:

```go
// Antipattern: inbox guard inside domain service method
func (s *UserService) CreateUserFromWorkspace(ctx context.Context, input CreateUserInput) error {
    if input.EventID != "" && s.inboxRepo != nil {
        isDup, err := s.inboxRepo.TryInsert(ctx, input.EventID)
        if isDup {
            return nil // Skip duplicate
        }
    }
    // Proceed with business mutations...
}
```

### Failure Modes:

1. **Silent Data Loss (Non-Atomic Inbox Check)**:
   If `TryInsert` runs in an independent auto-committing transaction, it commits the `event_id` to PostgreSQL immediately. If the subsequent `CreateUser` call fails due to a network timeout, RabbitMQ retries the event. On retry, `TryInsert` sees the event ID, flags it as duplicate, and skips execution. The message is ACKed, and the user is never created.

2. **The PostgreSQL Transaction Abortion Trap (`pq: 23505`)**:
   Attempting to solve this by wrapping `TryInsert` and `CreateUser` in a single `sql.Tx` while relying on catching unique constraint violations causes transaction invalidation:

   ```go
   // Antipattern: relying on unique constraint violation inside an active transaction
   _, err := tx.ExecContext(ctx, "INSERT INTO public.inbox (event_id) VALUES ($1)", eventID)
   if err != nil {
       var pqErr *pq.Error
       if errors.As(err, &pqErr) && pqErr.Code == "23505" {
           return true, nil // Suppress duplicate error in Go
       }
   }
   ```

   **PostgreSQL Engine Rule**: When any statement inside an open transaction block raises an error (such as unique violation `23505`), PostgreSQL marks the entire transaction as aborted. Even if the Go application catches and ignores the error, subsequent queries or `tx.Commit()` will fail with:
   ```text
   pq: current transaction is aborted, commands ignored until end of transaction block
   ```

3. **Layer Bleed**:
   AMQP event deduplication is a transport-level concern. Injecting inbox repositories into domain services forces business logic to manage messaging deduplication tables.

---

## 2. The Solution: `ON CONFLICT DO NOTHING` and `RowsAffected`

To check duplicates inside a transaction without triggering an SQL exception, use `ON CONFLICT DO NOTHING` and inspect `RowsAffected()`:

```sql
INSERT INTO public.inbox (message_id, consumer_name, event_type, processed_at)
VALUES ($1, $2, $3, NOW())
ON CONFLICT (message_id) DO NOTHING;
```

```go
func (r *InboxRepository) TryClaim(ctx context.Context, exec DBExecutor, msgID, consumer, evtType string) (bool, error) {
    query := `
        INSERT INTO public.inbox (message_id, consumer_name, event_type, processed_at)
        VALUES ($1, $2, $3, NOW())
        ON CONFLICT (message_id) DO NOTHING;
    `
    res, err := exec.ExecContext(ctx, query, msgID, consumer, evtType)
    if err != nil {
        return false, fmt.Errorf("inbox insert failed: %w", err)
    }

    rows, err := res.RowsAffected()
    if err != nil {
        return false, err
    }
    // rows == 1: new event claimed; rows == 0: duplicate skipped cleanly without error
    return rows == 1, nil
}
```

Because PostgreSQL does not raise an exception on conflict, the transaction state remains valid.

---

## 3. Clean Architecture Placement

In accordance with our layer boundaries, the inbox claim lives in **Layer 1** (`consumer/`), not Layer 2 (`service/`):

```text
WorkspaceInitiatedConsumer (Layer 1)
  │
  └─► txManager.WithTransaction(ctx, func(txCtx) error {
        // 1. Claim event idempotently inside transaction
        claimed, err := inboxService.ClaimEvent(txCtx, event.ID, "user-service")
        if !claimed {
            return nil // Duplicate: commit empty tx and ACK
        }

        // 2. Execute pure domain logic
        return userService.CreateUser(txCtx, event.Payload)
      })
```

---

## 4. Architectural Invariants & Operational Trade-offs

- **Transaction Integrity**: Duplicate detection relies on `ON CONFLICT DO NOTHING` and `RowsAffected()` to avoid triggering PostgreSQL transaction abortions.
- **Layer Cleanliness**: Deduplication tables and AMQP lifecycle coordination belong strictly in Layer 1 driving adapters.
- **Atomic Rollback**: If domain processing fails after an inbox insert, the outer transaction rolls back the claim, enabling clean retries.
