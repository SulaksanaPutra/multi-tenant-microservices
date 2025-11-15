# Transient Network Drops and Cache Invalidation Resiliency

*Handling socket disconnections, missed AMQP broadcasts, and reconnection cache eviction policies.*

---

## 1. The Failure Mode: Missed Broadcasts During Network Flaps

Using temporary exclusive queues on an AMQP fanout exchange ensures all active replicas receive cache invalidation messages under normal conditions.

However, during a transient network blip or broker failover:

```text
[ tenant-service ]                           [ RabbitMQ Broker ]                      [ order-service Replica 2 ]
       │                                              │                                          │
       │                                              │   (Network Blip / Socket Drop)           │
       │                                              │ ──────────────────────────────────────► X (Queue amq.gen-2 auto-deleted)
       │                                              │                                          │
  Tenant DB location changes                          │                                          │
       │                                              │                                          │
  Publish tenant.infrastructure_changed ────────────► │                                          │
                                                      │ ──► Delivered to Replica 1 (Success)     │ (Replica 2 offline)
                                                      │ ──► Message DROPPED for Replica 2        │
                                                      │                                          │
                                                      │     (Replica 2 Reconnects)               │
                                                      │ ◄─────────────────────────────────────── │
                                                      │     Declares NEW queue amq.gen-99        │
                                                      │                                          │
                                                      │                                    Incoming Request
                                                      │                                          │
                                                      │                                    Cache HIT in Replica 2
                                                      │                                    (Uses STALE DSN)
```

### The Invalidation Gap:
1. `Replica 2`'s TCP socket drops.
2. RabbitMQ auto-deletes its exclusive queue.
3. While `Replica 2` is re-establishing its connection, a cache invalidation event occurs.
4. `Replica 2` reconnects and binds a new queue, but it has missed the event that occurred while it was disconnected. Its local memory cache retains the old database location, leading to split-brain writes.

---

## 2. The Defensive Rule: Invalidate on Reconnection

Because ephemeral queues cannot recover messages published while disconnected, consumers must follow an explicit policy:

> **Whenever a consumer re-establishes a dropped connection to RabbitMQ, it must assume invalidation messages were missed while offline and purge its local caches.**

---

## 3. Implementation with Reconnect Signals

In our RabbitMQ infrastructure client (`rabbitmq/client.go`), reconnection notifications are exposed via a channel:

```go
type Client struct {
    reconnectChan chan struct{}
}

func (c *Client) NotifyReconnect() <-chan struct{} {
    return c.reconnectChan
}
```

The consumer listens for reconnect signals in a background goroutine:

```go
func (c *InfrastructureChangedConsumer) Start(ctx context.Context) {
    go func() {
        for {
            select {
            case <-ctx.Done():
                return
            case <-c.rabbitClient.NotifyReconnect():
                log.Println("RabbitMQ reconnected: purging local routing and pool caches")
                c.routingRegistry.PurgeAll()
                c.poolRegistry.PurgeAll()
            }
        }
    }()
    // Start standard AMQP message consumption...
}
```

Purging caches upon reconnection ensures that subsequent requests perform a fresh routing lookup, preventing stale cache entries from persisting after network interruptions.

---

## 4. Architectural Invariants & Operational Trade-offs

- **Reconnect Invalidation Guarantee**: Reconnecting after network interruption triggers an immediate cache purge to prevent stale routing entries.
- **Fail-Safe Fresh Lookups**: Transient network blips trade off brief cold-cache latency spikes for absolute cross-replica routing consistency.
- **Lifecycle Decoupling**: Reconnect listeners run in autonomous background loops decoupled from individual request contexts.
