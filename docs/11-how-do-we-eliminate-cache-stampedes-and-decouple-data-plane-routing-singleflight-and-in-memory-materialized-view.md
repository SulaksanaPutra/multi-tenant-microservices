# Eliminating Cache Stampedes: Singleflight and In-Memory Materialized Views

*Mitigating hot-path control plane bottlenecks and request coalescing in multi-tenant data routing.*

---

## 1. The Bottleneck: Synchronous Control-Plane Lookups

In earlier iterations of `order-service`, tenant connection pools were resolved dynamically. When a request arrived with an incoming tenant header, `order-service` looked up the tenant's database connection string in its local cache (`PoolRegistry`).

On a cache miss (or when an idle connection pool was evicted after a TTL timeout), `order-service` executed a **synchronous HTTP call** back to `tenant-service`:

```text
Client Request ──► [ order-service ] ──(Cache Miss)──► HTTP GET /internal/tenants/:id/... ──► [ tenant-service ]
                                                                                                     │
                                                                                                     ▼
                                                                                             [ tenantManagerDB ]
```

### Failure Modes Under Concurrency:
1. **Global Lock Contention**:
   If the HTTP call was executed while holding an internal write lock (`sync.RWMutex.Lock()`), a latency spike on one tenant's resolution blocked all concurrent cache lookups across all other tenants.
2. **The Thundering Herd**:
   When a high-traffic tenant's cache entry expired, hundreds of concurrent requests simultaneously experienced a cache miss, dispatching identical HTTP requests to `tenant-service` and overwhelming the control plane.
3. **Cascading Service Outages**:
   A minor slowdown or temporary network blip on `tenant-service` immediately propagated into `order-service`, degrading the entire customer-facing order flow.

---

## 2. Request Coalescing with `singleflight`

To prevent duplicate outbound requests during cache misses, we introduce request coalescing using Go's `golang.org/x/sync/singleflight`:

```text
1,000 Concurrent Requests ──► [ PoolRegistry.GetOrFetch() ]
(Cache Miss for Tenant A)                    │
                                             ▼
                                  [ singleflight.Group.Do ]
                                             │
                       ┌─────────────────────┴─────────────────────┐
                       │  Only 1 Worker Executes fetchDSN()        │
                       │  (OUTSIDE global sync lock)               │
                       └─────────────────────┬─────────────────────┘
                                             │
                                             ▼
                               Returns result to all 1,000
                               callers simultaneously
```

### Implementation Notes:
- **Lock Extraction**: The network call (`fetchDSN`) executes outside of the cache's mutex lock. Only the resulting pool assignment acquires the write lock.
- **Context Detachment**: If 500 requests share a singleflight call and the first client disconnects, canceling caller #1's context could fail all waiting callers. Using a detached context with its own timeout ensures in-flight resolution runs to completion:

```go
v, err, _ := r.sfGroup.Do(tenantID, func() (interface{}, error) {
    // Re-check cache under read lock to handle race conditions
    r.mu.RLock()
    entry, ok := r.entries[tenantID]
    r.mu.RUnlock()
    if ok {
        return entry, nil
    }

    // Execute lookup with independent timeout context
    lookupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
    defer cancel()

    dsn, err := r.fetchDSN(lookupCtx, tenantID)
    if err != nil {
        return nil, err
    }

    return r.initPool(tenantID, dsn)
})
```

---

## 3. Decoupling with In-Memory Materialized Views

While `singleflight` prevents thundering herds, it still relies on a synchronous HTTP call on cache misses.

The long-term solution is eliminating synchronous HTTP calls entirely using an **In-Memory Materialized View** (`RoutingRegistry`):
- `order-service` subscribes to AMQP events (`workspace.initiated`, `tenant.routing.updated`).
- Routing metadata is received asynchronously and cached in memory at startup and during updates.
- Connection resolution looks up local memory with zero external network calls on the request hot path.

---

## 4. Architectural Invariants & Operational Trade-offs

- **Request Coalescing Guarantee**: `singleflight` ensures that no matter how many concurrent requests miss the routing cache, exactly one outbound lookup is executed per tenant.
- **Context Shielding**: Shared fetch routines use isolated execution contexts to prevent individual client disconnects from aborting in-flight lookups.
- **Decoupled Hot Path**: Moving routing metadata to an in-memory materialized view removes inter-service network dependencies from transaction execution paths.
