# Broker Disconnect Detection and Context-Driven Cache Invalidation

*Engineering notes on connection context lifecycles, cache eviction timing, and fail-fast load shedding in Go.*

---

## 1. Context and Audit of Reconnection Mechanisms

In our multi-tenant microservices, local memory caches (`RoutingRegistry` and `PoolRegistry`) rely on AMQP fanout broadcasts to stay synchronized. When a tenant database changes location, `tenant-service` publishes an invalidation event.

Under network disruptions, an edge case exists: if a replica's TCP connection to RabbitMQ drops, its exclusive auto-delete queue is removed by the broker. Any invalidation broadcasts emitted while disconnected are lost.

An earlier implementation purged local caches *after* a successful reconnection. However, an audit revealed subtle issues with this timing:

1. **Stale Cache Window During Reconnect Retries**:
   If RabbitMQ was down for 30 seconds, the reconnect loop ran for 30 seconds. During that entire 30-second outage window, incoming HTTP requests continued to read potentially stale database routes from memory.
2. **Channel Signal Drops**:
   Relying on non-blocking channel sends (`select { case ch <- sig: default: }`) meant that if the consumer goroutine was busy when reconnection occurred, the signal was dropped, leaving stale cache entries in memory.

---

## 2. Context Lifecycles and Disconnect-Phase Eviction

### Context Cancellation Over Channel Signaling
Rather than passing discrete event notifications across channels, connection state represents a **lifecycle**. We bind connection availability directly to Go's `context.Context`:

```go
type Client struct {
    mu       sync.RWMutex
    connCtx  context.Context
    cancel   context.CancelFunc
}

func (c *Client) watchConnection(conn *amqp.Connection) {
    for {
        closeErr := <-conn.NotifyClose(make(chan *amqp.Error, 1))

        c.mu.Lock()
        c.cancel() // Cancel active connection context immediately on socket drop
        c.mu.Unlock()

        log.Printf("RabbitMQ connection dropped: %v", closeErr)
        // Initiate background reconnect loop...
    }
}
```

Context cancellation is idempotent, persistent, and non-blocking. Any goroutine checking `<-c.connCtx.Done()` detects the disconnect immediately.

### Shifting Cache Eviction from Reconnect to Disconnect
By reacting to context cancellation, cache eviction shifts from the reconnect phase to the **disconnect phase**:

```text
Old Sequence:
Socket drops ──► [Retry loop: 30s] ──► Reconnect succeeds ──► PurgeAll()
                 │                                        │
                 └────── Stale Cache Serving Window ──────┘

Refined Sequence:
Socket drops ──► cancel() fires ──► PurgeAll() immediately ──► [Retry loop starts]
                 │                  │
                 └── Window: <1ms ──┘
```

The moment the socket drops, caches are purged immediately. Incoming requests during the outage window will not use stale cached DSNs; they fail fast or fall back to safe defaults rather than writing to old database locations.

---

## 3. Architectural Invariants & Operational Trade-offs

- **Immediate Disconnect Eviction**: Memory routing and pool caches are purged at socket disconnect time (`cancel()`) rather than waiting for reconnection, preventing stale routing during outages.
- **Fail-Fast Over Stale Reads**: During broker disconnects, the system trades off read availability on tenant routes for strict isolation guarantees, failing fast rather than routing to obsolete schemas or dedicated containers.
- **Idempotent Context Cancellation**: Connection lifecycle is modeled via `context.Context` rather than non-blocking Go channel signals, preventing dropped signals during consumer saturation.
