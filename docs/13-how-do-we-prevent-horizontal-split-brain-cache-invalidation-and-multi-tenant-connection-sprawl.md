# How Do I Prevent "Split-Brain" Data and Connection Sprawl When Scaling Up?

*Engineering Notes on RabbitMQ Topologies, Socket Limits, and Double-Checked Locking in Go*

---

## 1. Initial Setup and Scaling Challenges

When building a microservice locally, I usually run a single instance of `order-service`. In this single-instance environment, my initial design worked fine: whenever a tenant's database infrastructure changed, `tenant-service` emitted an event. `order-service` consumed the RabbitMQ message, cleared its local memory cache, and routed subsequent queries to the new database location (DSN).

However, in production, a single instance cannot handle high traffic. We scale horizontally by running multiple copies (replicas) of our service behind a load balancer.

When I simulated running multiple `order-service` replicas locally, two critical issues surfaced:

1. **The Split-Brain Bug:** Only one replica received the cache invalidation message and updated its memory. The remaining replicas kept writing customer data to the old database.
2. **Connection Sprawl:** Creating database connection pools for every tenant across every replica quickly exhausted Linux socket descriptors (`EMFILE`) and crashed PostgreSQL with `FATAL: sorry, too many clients already`.

To visualize why this happened, consider a simple analogy:

### The 3 Workers Analogy
Imagine three identical workers (Replicas #1, #2, and #3). Each worker keeps a personal notepad (an in-memory cache) to remember which database address belongs to which customer.

* **The Shared Mailbox Problem:** When a database changed location, the system sent a letter to a shared mailbox (a named queue). Because all three workers shared a single mailbox, only the first worker who checked the mail (Replica #1) retrieved the letter.
* **The Split-Brain State:** Replica #1 read the letter and updated its notepad. Replicas #2 and #3 never saw the letter. When subsequent customer requests came in, Replica #1 routed to the new database, while Replicas #2 and #3 relied on their outdated notepads and wrote to the old database.

---

## 2. Distributed Systems Mechanics and Architectural Realizations

To address these issues, I had to review several assumptions about messaging topology and database connection pools.

### AMQP Routing Mechanics: The Post Office Model
RabbitMQ routes messages using a strict Post Office model:

* **The Exchange:** The central sorting facility (`company.events`).
* **The Routing Key:** The destination address (`tenant.infrastructure_changed`).
* **The Queue:** The physical mailbox.

If all 3 `order-service` replicas listen to the exact same named queue (e.g., `order_service_infrastructure_changed`), they act like three workers sharing one mailbox. RabbitMQ delivers a message to the queue, one replica processes it, and RabbitMQ deletes it. This is the **Competing Consumer pattern**—useful for distributing work, but problematic for cache invalidation where every replica needs the message.

### Connection Sprawl and Resource Consumption
As database connections multiplied, I looked into whether idle connections actually consume resources:
> *"If a connection is idle and no queries are running, does it still impact the system?"*

Idle connections consume system resources. Even when no SQL query is executing, an open TCP connection holds a physical network socket tracked by an operating system file descriptor. When hundreds of tenant databases maintain idle connection pools across multiple scaled replicas, sockets accumulate until the operating system runs out of file descriptors (`EMFILE`), or PostgreSQL returns an error:

```text
FATAL: sorry, too many clients already
```

### Replica Limits vs. Pool Management
This raised another design question:
> *"Why not just limit the number of worker replicas we spawn instead of tuning connection pools?"*

Limiting worker replicas restricts horizontal scaling capacity. If traffic spikes, an artificially capped worker pool leads to request queuing and high latency. We need to scale workers horizontally, which means managing connection pool lifecycles per instance rather than capping service instances.

---

## 3. Fixing Flaw #1: Cache Invalidation via Fanout Broadcast

**Solution: Dedicated Mailboxes Per Replica**

Instead of sharing a single named queue across all replicas, we use an **Exclusive Anonymous Fanout Broadcast**.

By passing an empty string `""` to `QueueDeclare`, RabbitMQ creates a unique, private, temporary mailbox for that specific replica process (e.g., `amq.gen-12345`) that automatically deletes when the replica disconnects.

```go
// 1. Declare an unnamed, exclusive queue for this specific replica instance
q, err := client.Channel.QueueDeclare(
    "",    // Empty string generates a unique server-assigned queue name
    false, // non-durable
    true,  // auto-delete (removes queue if replica disconnects)
    true,  // exclusive to this connection
    false, // no-wait
    nil,
)

// 2. Bind this private mailbox to the Exchange for broadcast invalidation
client.Channel.QueueBind(q.Name, domain.RoutingKeyInfraChanged, domain.ExchangeCompanyEvents, false, nil)
```

When a `tenant.infrastructure_changed` event is published to the Fanout Exchange, RabbitMQ sends a copy to every replica's private queue. All replicas receive the message and clear their local caches simultaneously.

> **Note:** Fanout Broadcast is used specifically for cache invalidation. For DDL schema migrations (`InfrastructureProvisionedConsumer`), we keep the shared named queue (`order_service_infrastructure_provisioned`) so migrations execute exactly once rather than running concurrently on every replica.

---

## 4. Fixing Flaw #2: Connection Sprawl and Double-Checked Locking

To manage connection sprawl, I updated per-tenant `*sql.DB` parameters in `PoolRegistry`:
* Set `SetMaxOpenConns` to `3`.
* Set `SetMaxIdleConns` to `1`.
* Set `SetConnMaxIdleTime` to `30s`.
* Set pool `defaultTTL` to `3m`.
* Set background `reaperInterval` to `1m`.

### The Background Reaper and Race Conditions
To clean up inactive pools, I added a background worker called the Reaper to close idle connections. However, the initial implementation contained a race condition:

1. The Reaper scanned pools under a Read Lock (`RLock()`) and identified an idle pool for Tenant A.
2. The Reaper released the Read Lock to acquire a Write Lock (`Lock()`) for deletion.
3. **The Race Window:** Between releasing `RLock()` and acquiring `Lock()`, an incoming HTTP request arrived for Tenant A, accessed the pool, and updated `entry.lastUsed = time.Now()`.
4. The Reaper acquired `Lock()` and deleted the active pool while the HTTP request was using it.

### The Fix: Double-Checked Locking
To fix this, I added a double-check inside the Write Lock. Before deleting the map entry, the Reaper verifies `entry.lastUsed` a second time to ensure no request accessed the pool during the lock transition:

```go
func (r *PoolRegistry) reap() {
    cutoff := time.Now().Add(-r.ttl)

    // Step 1: Collect candidate tenant IDs under RLock
    r.mu.RLock()
    var candidates []string
    for tenantID, entry := range r.entries {
        if entry.lastUsed.Before(cutoff) {
            candidates = append(candidates, tenantID)
        }
    }
    r.mu.RUnlock()

    if len(candidates) == 0 {
        return
    }

    // Step 2: Acquire Write Lock and DOUBLE-CHECK before deleting
    for _, tenantID := range candidates {
        var dbToClose *sql.DB

        r.mu.Lock()
        entry, ok := r.entries[tenantID]
        // DOUBLE-CHECK: Is lastUsed still before the cutoff?
        if ok && entry.lastUsed.Before(cutoff) {
            delete(r.entries, tenantID) // Safe to delete map entry
            dbToClose = entry.db
        }
        r.mu.Unlock()

        // Step 3: Gracefully close connection outside the write lock
        if dbToClose != nil {
            r.closePoolGracefully(dbToClose, tenantID)
            log.Printf("PoolRegistry Reaper: Evicted idle pool for tenant '%s' (double-checked)", tenantID)
        }
    }
}
```

---

## 5. Architectural Summary

| Component | Initial Approach | Updated Fix |
| :--- | :--- | :--- |
| **Cache Invalidation** | Named Queue (Shared queue; only 1 replica evicted cache) | Empty Queue String `""` for Fanout Broadcast (Private queue per replica) |
| **DDL Migrations** | Named Queue (Competing Consumer) | Kept as Shared Named Queue (Migrations execute once) |
| **Connection Pools** | 10 conns / pool, 15m TTL | 3 conns / pool, 30s idle timeout, 3m TTL |
| **Reaper Lock Safety** | Single write lock during sweep | Read-lock candidate scan + Double-check write lock before deletion |
