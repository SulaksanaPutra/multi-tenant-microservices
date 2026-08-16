# Resilient Consumer Side Effects: Preventing Lost Notifications

*Resolving the dual-phase post-commit gap, state-machine aware idempotency guards, and reliable notification delivery.*

---

## 1. The Dual-Phase Post-Commit Gap

In event-driven microservices, consumers often trigger external side effects like sending emails or dispatching third-party webhooks.

In accordance with Clean Architecture standards, **database transactions must never hold open connections across external network I/O** (such as calling an SMTP server or external API). This splits consumer processing into two phases:

```text
[ RabbitMQ Message ]
         │
         ▼
┌────────────────────────────────────────────────────────┐
│ Phase 1: Database Transaction (Unit of Work)           │
│   1. INSERT INTO inbox (event_id) ON CONFLICT DO NOTHING│
│   2. Evaluate barrier events                           │
│   3. Insert notification record (status = 'PENDING')   │
│   4. COMMIT Transaction                                │
└────────────────────────────────────────────────────────┘
         │
         ▼
┌────────────────────────────────────────────────────────┐
│ Phase 2: External Side Effect (Non-Transactional)      │
│   1. Dispatch email via SMTP server                    │
│   2. UPDATE notifications SET status = 'SENT'          │
└────────────────────────────────────────────────────────┘
         │
         ▼
[ RabbitMQ ACK ]
```

### The Failure Mode:
Consider what happens if the consumer crashes or times out during Phase 2:
1. Phase 1 committed: `inbox` has the `event_id`, and `notifications` has a row with `status = 'PENDING'`.
2. The consumer crashes while talking to the SMTP server. The message was never ACKed.
3. RabbitMQ redelivers the unacknowledged message to another consumer instance.
4. On redelivery, Phase 1 executes `ClaimEvent(event_id)`. Because the `event_id` is already in the `inbox` table, the claim returns `false` (duplicate detected).
5. A naive consumer skips duplicate events and sends an ACK.
6. **Result**: The email was never sent, but the message was acknowledged and discarded. The notification remains stuck in `PENDING` indefinitely.

---

## 2. State-Machine Aware Idempotency

To prevent lost notifications, the inbox guard must check the domain entity's state before discarding duplicate events:

```go
func (c *NotificationConsumer) HandleMessage(ctx context.Context, msg amqp.Delivery) error {
    var event UserRegisteredEvent
    _ = json.Unmarshal(msg.Body, &event)

    var notification *Notification
    err := c.txManager.WithTransaction(ctx, func(txCtx context.Context) error {
        claimed, err := c.inboxRepo.TryClaim(txCtx, event.ID, "notification-service")
        if err != nil {
            return err
        }

        if !claimed {
            // Event was previously claimed: check domain state
            existing, err := c.notificationRepo.FindByEventID(txCtx, event.ID)
            if err != nil {
                return err
            }
            if existing != nil && existing.Status == StatusPending {
                // Side effect previously failed or crashed: resume Phase 2
                notification = existing
                return nil
            }
            // Already sent: safe to skip
            return nil
        }

        // New event: create pending record
        notification, err = c.notificationRepo.Create(txCtx, event.ToNotification())
        return err
    })

    if err != nil {
        return err
    }

    if notification == nil || notification.Status == StatusSent {
        return nil // Completed
    }

    // Phase 2: Dispatch external side effect outside the transaction
    if err := c.mailer.Send(notification.Recipient, notification.Subject, notification.Body); err != nil {
        log.Printf("SMTP delivery failed for notification %s: %v", notification.ID, err)
        return err // Triggers NACK and backoff retry
    }

    // Mark completed in database
    return c.notificationRepo.MarkSent(ctx, notification.ID)
}
```

---

## 3. Background Recovery Sweeper

Even with state-aware guards, network failures or process restarts can leave records in `PENDING` if a message is delayed or dead-lettered.

A background sweeper runs periodically to catch and retry stuck notifications:

```go
func (s *NotificationSweeper) Sweep(ctx context.Context) {
    // Find notifications stuck in PENDING longer than 5 minutes
    stuck, err := s.repo.FindStuckPending(ctx, 5*time.Minute)
    if err != nil {
        return
    }

    for _, n := range stuck {
        if err := s.mailer.Send(n.Recipient, n.Subject, n.Body); err != nil {
            s.repo.IncrementRetry(ctx, n.ID, err.Error())
        } else {
            s.repo.MarkSent(ctx, n.ID)
        }
    }
}
```

---

## 4. Architectural Invariants & Operational Trade-offs

- **Transaction-I/O Separation**: Database transactions commit state before executing external network calls (SMTP, SMS, push), preserving database connection pools.
- **State-Aware Idempotency**: Duplicate event delivery checks the entity status before discarding, allowing interrupted side effects to resume safely.
- **Dual-Layer Delivery Guarantee**: Combining message redelivery handling with a background recovery sweeper ensures notifications are dispatched even under consumer crashes or unrecoverable broker failures.
