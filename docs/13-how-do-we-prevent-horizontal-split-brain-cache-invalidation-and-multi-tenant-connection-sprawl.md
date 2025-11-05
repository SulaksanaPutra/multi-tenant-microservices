# Horizontal Scale: Cache Invalidation and Connection Sprawl

*Managing cache synchronization across service replicas and preventing connection exhaustion in multi-tenant PostgreSQL.*

---

## 1. Challenges in Multi-Replica Deployments

When running a single instance of a service, in-memory caching is straightforward: when tenant database routing updates, the instance clears its local cache and connects to the new target.

However, when scaling horizontally across multiple replicas behind a load balancer, two distributed systems issues arise:

1. **Split-Brain Caches**:
   If an invalidation message is sent to a shared queue, only one replica consumes it. The remaining replicas continue serving requests using stale in-memory routing entries, writing customer data to old database locations.
2. **Connection Sprawl**:
   Each replica independently establishes connection pools to tenant databases. If 10 service replicas each maintain connection pools to 100 tenant databases, the database cluster must support thousands of concurrent connections, risking socket exhaustion (`EMFILE`) or PostgreSQL connection limits:
   ```text
   FATAL: sorry, too many clients already
   ```

---

## 2. Invalidation via Fanout Broadcasts

To ensure every replica invalidates its local cache, routing updates must not use standard competing-consumer queues. Instead, we use an **AMQP Fanout Exchange** with exclusive auto-delete queues:

```text
[ tenant-service ] ──(Publishes tenant.infrastructure_changed)──► [ amq.fanout Exchange ]
                                                                       │
                         ┌─────────────────────────────────────────────┼─────────────────────────────────────────────┐
                         ▼                                             ▼                                             ▼
             [ Queue amq.gen-rep1 ]                        [ Queue amq.gen-rep2 ]                        [ Queue amq.gen-rep3 ]
             (Exclusive / Auto-Delete)                     (Exclusive / Auto-Delete)                     (Exclusive / Auto-Delete)
                         │                                             │                                             │
                         ▼                                             ▼                                             ▼
                 [ Replica 1 ]                                 [ Replica 2 ]                                 [ Replica 3 ]
                 (Purges Cache)                                (Purges Cache)                                (Purges Cache)
```

### Queue Declaration Pattern
```go
// Declare an exclusive, temporary queue for this specific replica process
q, err := ch.QueueDeclare(
    "",    // RabbitMQ assigns an automatic unique name (e.g. amq.gen-xyz)
    false, // non-durable
    true,  // auto-delete when consumer disconnects
    true,  // exclusive to this connection
    false, // no-wait
    nil,
)
if err != nil {
    return err
}

// Bind to fanout exchange
err = ch.QueueBind(q.Name, "", "tenant.events.fanout", false, nil)
```

When any replica dies or restarts, RabbitMQ automatically tears down its queue, preventing orphaned queues from accumulating in the broker.

---

## 3. Mitigating Connection Sprawl

To keep connection counts sustainable across multiple replicas:

1. **Conservative Pool Limits**:
   Each dedicated tenant pool is configured with conservative limits:
   ```go
   db.SetMaxOpenConns(5)
   db.SetMaxIdleConns(2)
   db.SetConnMaxLifetime(15 * time.Minute)
   ```
2. **Idle Pool Eviction**:
   A background sweeper runs periodically. If a tenant pool has not been accessed for 30 minutes, it is closed and evicted from memory:
   ```go
   func (r *PoolRegistry) EvictIdlePools(cutoff time.Duration) {
       r.mu.Lock()
       defer r.mu.Unlock()

       now := time.Now()
       for tenantID, entry := range r.entries {
           if now.Sub(entry.lastAccessed) > cutoff {
               entry.db.Close()
               delete(r.entries, tenantID)
           }
       }
   }
   ```

---

## 4. Architectural Invariants & Operational Trade-offs

- **Fanout Invalidation Invariant**: Cache invalidation messages must be broadcast to all instances simultaneously using exclusive auto-delete queues, never competing consumer queues.
- **Resource Reclaim**: Dead replica queues are automatically cleaned up by RabbitMQ upon connection drop, avoiding orphaned broker queues.
- **Connection Ceiling**: Multi-tenant pools trade off minor connection acquisition latency for strict connection ceilings that prevent socket descriptor exhaustion.
