# How We Prevented Cache Invalidation Loss During Network Flaps in RabbitMQ

*Engineering Notes on Socket Disconnections, Missed AMQP Broadcasts, and Reconnect Eviction Policies in Go*

---

## 1. Initial Setup and Network Flap Risks

In [Document 13: Preventing Horizontal Split-Brain and Connection Sprawl](./13-how-do-we-prevent-horizontal-split-brain-cache-invalidation-and-multi-tenant-connection-sprawl.md), I addressed cache invalidation across replicas by giving every `order-service` instance its own private, temporary queue (an exclusive, auto-delete queue).

When a tenant's database infrastructure changed, `tenant-service` broadcast a `tenant.infrastructure_changed` event. RabbitMQ delivered a copy to every replica's private queue, and each replica cleared its local memory caches (`RoutingRegistry` and `PoolRegistry`).

This design worked under normal operating conditions. However, under unstable network conditions with brief socket disconnections, an edge case emerged: **a transient network drop could cause missed broadcast events and stale cache states.**

---

## 2. Step-by-Step Breakdown of a Network Flap

To understand how invalidation events are lost during brief connection drops, we need to consider how RabbitMQ manages exclusive auto-delete queues over TCP:

```text
[ tenant-service ]                           [ RabbitMQ Broker ]                      [ order-service Replica 2 ]
       │                                              │                                          │
       │                                              │   (Network Blip / Socket Drop)           │
       │                                              │ ──────────────────────────────────────► X (Queue amq.gen-2 auto-deleted)
       │                                              │                                          │
  Tenant DB location changes                          │                                          │
       │                                              │                                          │
  Publish tenant.infrastructure_changed ────────────► │                                          │
                                                      │ ──► Delivered to Replica 1 (Success)     │ (Replica 2 IS NOT BOUND)
                                                      │ ──► Message DROPPED for Replica 2        │
                                                      │                                          │
                                                      │     (Replica 2 Reconnects)               │
                                                      │ ◄─────────────────────────────────────── │
                                                      │     Declares NEW queue amq.gen-99        │
                                                      │                                          │
                                                      │                                    Incoming HTTP Request
                                                      │                                          │
                                                      │                                    Cache HIT in Replica 2
                                                      │                                    (Uses STALE DSN)
                                                      │                                          │
                                                      ▼                                          ▼
                                          [ WRITES TO OLD DB ]                       [ WRITES TO NEW DB ]
                                          └───────────────────────── SPLIT-BRAIN ──────────────┘
```

### Sequence of Events:

1. **Network Interruption:**  
   A brief TCP socket interruption occurs between `replica-2` and the RabbitMQ broker.
2. **Queue Deletion:**  
   Because `replica-2`'s queue was declared as `exclusive=true` and `autoDelete=true`, RabbitMQ automatically deletes the queue when the TCP connection drops.
3. **The Missed Broadcast Event:**  
   While `replica-2` is reconnecting, a tenant updates their database location. `tenant-service` broadcasts `tenant.infrastructure_changed`. Because `replica-2` has no queue bound to the exchange during that window, RabbitMQ cannot deliver the message to `replica-2`.
4. **Stale Cache State:**  
   `replica-2` completes reconnection and creates a new queue (`amq.gen-99`). However, because it missed the invalidation message while offline, its local memory cache still stores the old database location (DSN).
5. **Split-Brain Writes:**  
   Subsequent HTTP requests routed to `replica-1` write to the new database, while requests routed to `replica-2` write to the old database. Both return successful HTTP responses, causing inconsistent data state without throwing errors.

---

## 3. Invalidate on Reconnection

Since network interruptions can occur in distributed environments, the application needs to handle post-reconnection state explicitly.

I established the following rule for consumer reconnects:  
> **"Whenever a consumer reconnects to RabbitMQ, it must assume invalidation messages were missed while offline and immediately purge its local caches."**

To maintain a clean separation between network handling and domain logic, I split the implementation into two layers:

```text
┌─────────────────────────────────────────────────────────────────────────────┐
│ LAYER 1: rabbitmq.Client (Network Driver Layer)                             │
│  - Watches TCP connection state using conn.NotifyClose                      │
│  - Re-establishes connection using exponential backoff                      │
│  - Thread-safely updates AMQP Connection and Channel pointers               │
│  - Emits notification signal via NotifyReconnect() <-chan struct{}          │
└──────────────────────────────────────┬──────────────────────────────────────┘
                                       │
                                 NotifyReconnect()
                                       │
                                       ▼
┌─────────────────────────────────────────────────────────────────────────────┐
│ LAYER 2: Consumer Recovery Loops (Domain Layer)                             │
│                                                                             │
│ ┌────────────────────────────────────────┐ ┌──────────────────────────────┐ │
│ │ InfrastructureChangedConsumer          │ │ InfraProvisionedConsumer     │ │
│ │ - Calls PoolRegistry.PurgeAll()        │ │ - Re-declares durable queue  │ │
│ │ - Calls RoutingRegistry.PurgeAll()     │ │ - Re-binds to exchange       │ │
│ │ - Re-declares exclusive queue          │ │ - Resumes consuming          │ │
│ │ - Re-binds and resumes consuming       │ └──────────────────────────────┘ │
│ └────────────────────────────────────────┘                                  │
└─────────────────────────────────────────────────────────────────────────────┘
```

---

## 4. Code Implementation in `order-service`

### 1. Reconnection Driver ([client.go](../order-service/internal/infrastructure/rabbitmq/client.go))

The `rabbitmq.Client` struct manages the raw connection and channel. A background loop detects socket drops, handles reconnection attempts, and signals listeners when reconnected:

```go
type Client struct {
	mu          sync.RWMutex
	amqpURL     string
	Conn        *amqp.Connection
	Channel     *amqp.Channel
	reconnectCh chan struct{}
	isClosed    bool
}

func (c *Client) watchConnection() {
	for {
		c.mu.RLock()
		if c.isClosed {
			c.mu.RUnlock()
			return
		}
		conn := c.Conn
		c.mu.RUnlock()

		// Block until the TCP connection drops
		closeErr := <-conn.NotifyClose(make(chan *amqp.Error, 1))
		if c.isClosed {
			return
		}

		log.Printf("RabbitMQ Driver: Connection dropped (%v). Reconnecting...", closeErr)

		// Retry connection with backoff
		for {
			if err := c.connect(); err == nil {
				log.Println("RabbitMQ Driver: Reconnected successfully!")
				select {
				case c.reconnectCh <- struct{}{}:
				default:
				}
				break
			}
			time.Sleep(2 * time.Second)
		}
	}
}
```

### 2. Cache Purge Implementation ([pool_registry.go](../order-service/internal/registry/pool_registry.go) & [routing_registry.go](../order-service/internal/registry/routing_registry.go))

Both registries expose `PurgeAll()` methods so consumers can clear local state upon reconnection:

```go
// PoolRegistry.PurgeAll evicts and closes cached database connection pools
func (r *PoolRegistry) PurgeAll() {
	r.mu.Lock()
	toClose := make(map[string]*sql.DB)
	for tenantID, entry := range r.entries {
		toClose[tenantID] = entry.db
		delete(r.entries, tenantID)
	}
	r.mu.Unlock()

	for tenantID, db := range toClose {
		r.sfGroup.Forget(tenantID)
		r.closePoolGracefully(db, tenantID)
	}
}

// RoutingRegistry.PurgeAll clears cached tenant routing metadata
func (r *RoutingRegistry) PurgeAll() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.routes = make(map[string]RoutingMetadata)
}
```

### 3. Consumer Recovery Loop ([infrastructure_changed_consumer.go](../order-service/internal/consumer/infrastructure_changed_consumer.go))

When a network blip closes the channel, `InfrastructureChangedConsumer` waits for the reconnect signal, clears local memory caches, declares a new queue, and resumes consuming:

```go
select {
case <-ctx.Done():
    return
case <-c.client.NotifyReconnect():
    log.Println("InfrastructureChangedConsumer: Reconnect signal received. Purging local caches...")
    
    // Step 1: Wipe local cache to discard stale state from missed events
    c.poolRegistry.PurgeAll()
    c.routingRegistry.PurgeAll()

    // Step 2: Re-declare exclusive queue and bind to exchange
    qName, err := c.setupTopology()
    if err != nil {
        continue
    }
    c.queueName = qName
}
```

---

## 5. Summary of Architectural Changes

| Concern | Initial Approach | Updated Fix |
| :--- | :--- | :--- |
| **Connection Drops** | Consumer goroutine stopped on socket drop | Driver reconnects automatically in background |
| **Missed Invalidation Messages** | Stale DSN remained in memory indefinitely | Cache purged on reconnect; forces fresh fetch from `tenant-service` |
| **Data Consistency** | Concurrent writes to old and new databases | Cache state aligned across all replicas post-reconnect |
| **Code Structure** | Network retries mixed with business logic | Driver handles connection state; consumer handles domain recovery |
