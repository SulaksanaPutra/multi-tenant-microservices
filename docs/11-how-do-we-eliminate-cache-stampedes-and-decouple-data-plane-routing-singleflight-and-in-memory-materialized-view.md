# How Do We Eliminate Cache Stampedes and Decouple Data Plane Routing? Singleflight & In-Memory Materialized View

*An Engineering Deep Dive into Fixing Hot-Path Control Plane Gridlock, Implementing Request Coalescing with Context Shielding, and Building an Event-Driven In-Memory Materialized View in Go*

---

## 1. The Vulnerability: Synchronous Control-Plane Dependency in the Hot Path

In early iterations of my multi-tenant data plane (`order-service`), connection pools to tenant databases were dynamically opened on demand. When an incoming order request (`POST /api/orders` or `GET /api/orders`) arrived with a `tenant-x-id` header, `order-service` checked its local connection pool cache (`PoolRegistry`).

On a cache miss (or when the 15-minute TTL evicted an idle pool), `order-service` executed a **synchronous HTTP request** back to `tenant-service`:

```text
                                  SYNCHRONOUS HOT-PATH DEPENDENCY
                                               
Client Request ──► [ order-service ] ──(Cache Miss)──► HTTP GET /internal/tenants/:id/... ──► [ tenant-service ]
                                                                                                     │
                                                                                                     ▼
                                                                                             [ tenantManagerDB ]
```

### Why This Caused System-Wide Collapse Under Load

When evaluating system bottlenecks and concurrency safety under heavy load, I uncovered critical architectural vulnerabilities:

1. **The Global Lock Bottleneck:**
   Inside `PoolRegistry`, `fetchDSN()` (which initiated the HTTP call to `tenant-service`) was invoked **inside the global `sync.RWMutex` write lock (`r.mu.Lock()`)**. If `tenant-service` experienced a 3-second network latency spike for Tenant A, all cache misses for Tenants B, C, and D were completely serialized and blocked behind Tenant A's HTTP request!

2. **The Thundering Herd / Cache Stampede:**
   When a high-traffic tenant's 15-minute pool TTL expired (or when `order-service` restarted), 1,000+ concurrent order requests for that tenant simultaneously hit a cache miss. They all launched parallel HTTP requests to `tenant-service`, instantly overloading `tenantManagerDB`, exhausting socket descriptors, and bringing down the control plane.

3. **Data Plane / Control Plane Coupling:**
   A minor outage, lock contention, or slow query inside `tenant-service` immediately cascaded into a 100% transaction outage across `order-service`.

---

## 2. The Defensive Fix: Request Coalescing via `singleflight`

To immediately stop the Thundering Herd cache stampede, I implemented Go's `golang.org/x/sync/singleflight` package inside `PoolRegistry`.

```text
                     SINGLEFLIGHT REQUEST COALESCING IN POOLREGISTRY

1,000 Concurrent Requests ──► [ PoolRegistry.GetOrFetch() ]
(Cache Miss for Tenant A)                    │
                                             ▼
                                  [ singleflight.Group.Do ]
                                             │
                       ┌─────────────────────┴─────────────────────┐
                       │  Only 1 Worker Executes fetchDSN()        │
                       │  (OUTSIDE global r.mu.Lock())             │
                       └─────────────────────┬─────────────────────┘
                                             │
                                             ▼
                               Returns result to all 1,000
                               waiting callers simultaneously
```

### Unlocked DSN Resolution
I extracted `fetchDSN()` execution **outside of `r.mu.Lock()`**. Now, multiple tenants resolving DSNs concurrently never block each other on mutex acquisition.

### Context Shielding
If 1,000 requests are coalesced under caller #1's HTTP request context (`r.Context()`), and caller #1 closes their browser tab or cancels their HTTP connection, a naive `singleflight` implementation would cancel the ongoing fetch, causing all 999 waiting callers to fail with `context.Canceled`.

I shielded singleflight execution by detaching worker execution:
```go
v, err, _ := r.sfGroup.Do(tenantID, func() (interface{}, error) {
    // Double-check cache inside singleflight worker
    r.mu.RLock()
    entry, ok := r.entries[tenantID]
    r.mu.RUnlock()
    if ok {
        return fetchResult{db: entry.db, schemaName: entry.schemaName}, nil
    }

    // Execute fetchDSN lock-free
    db, schemaName, err := fetchDSN()
    if err != nil {
        return nil, err
    }

    r.mu.Lock()
    r.entries[tenantID] = &poolEntry{db: db, schemaName: schemaName, lastUsed: time.Now()}
    r.mu.Unlock()

    return fetchResult{db: db, schemaName: schemaName}, nil
})
```

---

## 3. The Architectural Cure: In-Memory `RoutingRegistry` Materialized View

While `singleflight` eliminates cache stampedes, `order-service` still depended on `tenant-service` via HTTP on cold cache misses. 

### Why I Rejected Redis
I initially considered introducing Redis to cache routing metadata. However, I rejected Redis because:
1. **Network Hop Retained:** Calling Redis over TCP still leaves network I/O in the hot path.
2. **Additional Failure Surface:** Redis partition or memory pressure causes `order-service` outage.
3. **Zero-Trust Secret Exposure:** Storing tenant DSNs across all tenants in Redis creates a centralized, high-value target for lateral movement.

### The In-Memory Materialized View Solution
Instead, I designed `order-service` to maintain a local **`RoutingRegistry` materialized view** directly in process memory (`map[string]RoutingMetadata`) synchronized asynchronously via RabbitMQ domain events (`tenant.order_db.ready` and `tenant.infrastructure_changed`).

```text
                                ZERO-NETWORK HOT PATH

Client Request ──► [ order-service ] ──(0ms Direct RAM Lookup)──► [ RoutingRegistry ]
                                                                       ▲
                                                             RabbitMQ Event Stream
                                                       (tenant.infrastructure_changed)
```

---

## 4. Concurrency & Synchronization Design

To safely handle high-concurrency reads (thousands of HTTP requests per second) alongside occasional RabbitMQ writes (tenant onboarding/upgrades) without map write panics:

1. **`sync.RWMutex` Synchronization:**
   - Reads acquire `RLock()` (non-blocking across concurrent HTTP worker goroutines).
   - Event updates acquire `Lock()` briefly to update/delete entries in `RoutingRegistry`.

```go
type RoutingMetadata struct {
	TenantID   string `json:"tenant_id"`
	DBHost     string `json:"db_host"`
	DBPort     int    `json:"db_port"`
	DBName     string `json:"db_name"`
	DBUser     string `json:"db_user"`
	SchemaName string `json:"schema_name"`
}

type RoutingRegistry struct {
	mu     sync.RWMutex
	routes map[string]RoutingMetadata
}

func (r *RoutingRegistry) Get(tenantID string) (RoutingMetadata, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	meta, ok := r.routes[tenantID]
	return meta, ok
}

func (r *RoutingRegistry) Set(meta RoutingMetadata) {
	if meta.TenantID == "" {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.routes[meta.TenantID] = meta
}
```

2. **Resolution Pipeline in `tenantDBResolver`:**
   - **Fast Path (0ms):** Query `RoutingRegistry.Get(tenantID)`. If hit, derive HMAC database password locally in memory and open pool. Zero HTTP requests made.
   - **Slow Path (Fallback):** If cold start or cache miss, fetch routing metadata via `singleflight`-coalesced HTTP call to `tenant-service` and populate `RoutingRegistry`.

---

## 5. Architectural Benefits & Metrics

| Metric / Scenario | Before (Synchronous HTTP) | After (Singleflight + Materialized View) |
| :--- | :--- | :--- |
| **Hot Path Latency** | 5ms - 500ms (Network dependent) | **0ms (Local RAM lookup)** |
| **Control Plane Outage Impact** | Global transaction failure | **Zero impact** (Order processing continues natively) |
| **1,000 Concurrent Misses** | 1,000 HTTP requests to `tenant-service` | **1 coalesced fetch** |
| **Global Lock Contention** | High (`fetchDSN` inside `r.mu.Lock()`) | **Zero** (`fetchDSN` lock-free) |
| **Secret Protection** | Plaintext credentials in network transit | Stateless HMAC derivation in memory |
