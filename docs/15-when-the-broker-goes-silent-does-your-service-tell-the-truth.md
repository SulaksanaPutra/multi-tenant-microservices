# When the Broker Goes Silent, Does Your Service Tell the Truth?

*Engineering Notes on Context Lifecycles, Cache Miss Throttling, Singleflight, and Fail-Fast Load Shedding in Go*

---

## 1. Context and Audit of Initial Implementation

In [Document 14: How We Prevented Cache Invalidation Loss During Network Flaps](./14-how-do-we-prevent-transient-network-split-brain-cache-invalidation-loss.md), I established a recovery policy for multi-tenant microservices:

> *When an `order-service` replica loses its TCP connection to RabbitMQ, its exclusive auto-delete queue is removed. Any broadcast events sent while offline are missed. Therefore, upon reconnecting, the replica must assume it missed invalidation events and purge its local caches (`RoutingRegistry` and `PoolRegistry`).*

While Document 14 addressed the core split-brain risk, a subsequent code audit revealed three technical issues in that initial implementation:

1. **Signal Loss via Non-Blocking Select:** The `NotifyReconnect()` channel used a `default` fallback branch. If the consumer was busy processing a request when reconnection completed, the signal was dropped and the cache remained unpurged.
2. **Purge Timing Window:** Cache eviction occurred *after* reconnection succeeded. During a multi-second reconnection retry loop, incoming HTTP requests continued serving stale DSNs from memory.
3. **Mutex Contention and Resource Cleanup:** `PurgeAll()` iterated through map entries while holding a global write lock, and dropped database handles were not closed explicitly, leading to potential socket leaks.

This document outlines the refactoring applied to address these issues.

---

## 2. Context Lifecycles and Disconnect-Phase Eviction

### Replacing Channel Signals with Context Cancellation
Connection state represents a lifecycle rather than an isolated event. I replaced non-blocking channel notifications with Go `context.Context` cancellation.

In `rabbitmq.Client`, I bound a `context.Context` and `context.CancelFunc` directly to the active TCP connection lifecycle:

```go
// Watcher detects socket drop and cancels the connection context immediately
func (c *Client) watchConnection() {
    for {
        closeErr := <-conn.NotifyClose(make(chan *amqp.Error, 1))

        c.mu.Lock()
        c.cancel() // Cancel the active connection context immediately
        c.readyCh = make(chan struct{}) // Reset ready state
        c.mu.Unlock()

        log.Printf("RabbitMQ connection dropped: %v", closeErr)
        // ... reconnect loop ...
    }
}
```

Context cancellation is idempotent and non-blocking. Regardless of consumer state, cancellation persists. When the consumer delivery loop evaluates `<-connCtx.Done()`, it exits and triggers `PurgeAll()`.

### Shifting Cache Eviction to Disconnect
In the initial implementation, cache eviction occurred after reconnection:

```text
Doc 14 Sequence:
Socket drops ──► [Retry loop: 2s backoffs for 30s] ──► Reconnect succeeds ──► PurgeAll()
                 │                                                       │
                 └───────────── Stale Cache Serving Window ──────────────┘
```

During the retry loop, incoming HTTP requests read stale DSNs from memory.

Tying cache invalidation to connection-lifetime context cancellation shifts eviction from the reconnect phase to the disconnect phase:

```text
Refined Sequence:
Socket drops ──► cancel() fires ──► Consumer catches Done() ──► PurgeAll() ──► [Retry loop starts]
                 │                                             │
                 └──────── Stale Window: Microseconds ─────────┘
```

The consumer detects connection drop within microseconds. Caches are cleared before the driver initiates its first reconnect attempt, preventing stale cache reads during the outage window.

---

## 3. Lock Contention and Socket Reclamation in `PurgeAll()`

The initial `PoolRegistry.PurgeAll()` method iterated through map entries while holding a write lock, calling `db.Close()` serially. This introduced two problems under load:

1. **Write-Lock Contention:** Traversing map entries under `r.mu.Lock()` blocked concurrent HTTP request goroutines attempting to acquire `r.mu.RLock()`.
2. **File Descriptor Leaks:** Relying on garbage collection to reclaim `*sql.DB` references does not close underlying TCP sockets immediately. Sockets remain in `ESTABLISHED` or `CLOSE_WAIT` states until OS file descriptors are exhausted (`EMFILE`).

### Fix: Map Swap and Asynchronous Cleanup
`PurgeAll()` now performs a map pointer swap under lock, releasing the mutex immediately. The stale map is then passed to a bounded queue (`cleanupQueue`) processed asynchronously by a background worker:

```go
func (r *PoolRegistry) PurgeAll() {
    r.mu.Lock()
    staleEntries := r.entries
    r.entries = make(map[string]*poolEntry) // O(1) map swap
    r.mu.Unlock()                          // Mutex released immediately

    select {
    case r.cleanupQueue <- staleEntries:
    default:
        // Overflow path for rapid network reconnection cycles
        go func(m map[string]*poolEntry) {
            for tenantID, entry := range m {
                r.sfGroup.Forget(tenantID)
                r.closePoolGracefully(entry.db, tenantID)
            }
        }(staleEntries)
    }
}
```

---

## 4. Managing Cache Miss Storms with Singleflight and Bounded Semaphores

When all replicas purge their caches following a network drop, subsequent HTTP requests cause cache misses. If thousands of requests arrive concurrently across distinct tenants, this can trigger parallel HTTP calls to `tenant-service`, overloading the control plane.

To manage this, I applied a two-part concurrency control mechanism:

### Part 1: Request Coalescing with `singleflight`
If multiple concurrent requests arrive for the same tenant during a cache miss, `singleflight` groups them so that only one HTTP request is executed against `tenant-service`. The remaining requests wait for that call to complete and reuse the result.

### Part 2: Fail-Fast Load Shedding with a Bounded Semaphore
To handle concurrent cache miss requests across *different* tenants, I added a bounded semaphore (`httpSemaphore`) limited to 50 concurrent outbound calls in `fetchRoutingFromService()`:

```go
select {
case r.httpSemaphore <- struct{}{}:
    defer func() { <-r.httpSemaphore }()
    // Fetch DSN from tenant-service
case <-ctx.Done():
    return registry.RoutingMetadata{}, ctx.Err()
default:
    // Fail fast to prevent worker thread starvation
    return registry.RoutingMetadata{}, fmt.Errorf(
        "tenantdb: control plane fetch limit reached, shedding load for tenant %s", tenantID,
    )
}
```

### Why Fail-Fast Load Shedding is Preferred Over Request Queuing
When evaluating how to handle excess requests beyond the semaphore limit, I considered queuing incoming requests on the server:

> *"Should excess requests wait in an internal queue until capacity opens up?"*

Queuing synchronous HTTP requests holds open HTTP worker goroutines and client connections. Under heavy load, queued requests consume memory, increase latency, and cause clients to time out while holding server resources.

Using fail-fast load shedding returns an immediate error (equivalent to `503 Service Unavailable`) when concurrency limits are hit:

* **Trade-off:** A subset of requests fail immediately during an un-cached traffic spike.
* **Benefit:** HTTP worker threads remain available to serve requests for tenants whose routing data is already cached.

### Client-Side Retries
When clients (web or mobile apps) receive a transient `503 Service Unavailable` response, they can execute an automatic retry after a short delay (e.g., 500ms). By the time the retry executes, previous requests have populated the cache, allowing the retried request to complete normally.

---

## 5. Implementation Comparison

| Subsystem / Concern | Doc 14 Implementation | Updated Refactored Implementation |
| :--- | :--- | :--- |
| **Invalidation Trigger** | `NotifyReconnect()` (after reconnect) | `<-connCtx.Done()` (on TCP drop) |
| **Signal Handling** | Non-blocking channel write | `context.Context` cancellation |
| **Reconnect Waiting** | Channel wait / Polling sleep | `WaitUntilReady()` closed-channel unblock |
| **Purge Lock Scope** | Iterated map entries under write lock | Map pointer swap under write lock + async cleanup |
| **Socket Reclamation** | Relied on garbage collection | Explicit `.Close()` via background queue worker |
| **Control Plane Throttling** | Unbounded HTTP fetch concurrency | Singleflight + Bounded Semaphore (Fail-Fast Load Shedding) |
