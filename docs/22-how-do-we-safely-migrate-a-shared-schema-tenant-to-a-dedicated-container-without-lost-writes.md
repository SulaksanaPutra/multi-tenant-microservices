# Live Migration of Shared Tenants to Dedicated Containers Without Lost Writes

*A four-phase Read-Only Pause saga: transactional outbox pausing, fanout freeze broadcasts, physical schema locks, and cutover.*

---

## 1. The Challenge of Live Tenant Upgrades

In hybrid multi-tenancy, standard tenants run in isolated schemas (`tenant_<id>_order_db`) on a shared PostgreSQL cluster. When an enterprise customer upgrades to a dedicated plan, their data must be transferred to a private PostgreSQL container without dropping or corrupting in-flight orders.

A naive copy-and-switch (`pg_dump | psql`) while the service is active causes data corruption:
- Orders committed while `pg_dump` is running are omitted from the dump.
- Cached connection pools continue directing writes to the old database.
- Asynchronous background outbox workers continue polling and publishing events from the old schema during the transfer.

To prevent lost writes without taking the entire microservice fleet offline, we implement a **Read-Only Pause Saga**.

---

## 2. The Four-Phase Migration Saga

```text
[ Admin Request ]  PUT /api/v1/tenants/me/plan  { plan: "dedicated" }
        │
        ▼
[ tenant-service ]
        │  1. UPDATE tenants SET status = 'MIGRATING'
        │  2. Publish tenant.infrastructure_locking (Fanout)       (Phase 1: In-Flight Lock)
        │  3. Publish workspace.initiated (Dedicated)              (Phase 2: Container Provisioning)
        ▼
[ order-service replicas ]        [ infra-provisioner ]
   Lock consumer sets status        Provisions private PostgreSQL container
   = "MIGRATING" in cache           │
        │                           ├─► 1. ALTER SCHEMA tenant_xxx RENAME TO ..._locked
        ├─► HTTP API: 423 Locked    ├─► 2. pg_dump shared | psql dedicated  (Phase 3: Data Migration)
        └─► Outbox Worker: Skips    ├─► 3. Boots container
                                    ▼
                               RabbitMQ: infrastructure.provisioned
                                    │
                                    ▼
[ order-service ] ────────────► Runs migrations on new container ──► tenant.order_db.ready
                                                                           │
                                                                           ▼
[ tenant-service ] ◄───────────────────────────────────────────────────────┘
   1. Updates routing metadata (Placement -> DEDICATED)
   2. Sets status = 'ACTIVE'
   3. Broadcasts tenant.infrastructure_changed (Fanout)                    (Phase 4: Traffic Resumption)
        │
        ▼
order-service replicas clear lock & route writes to new dedicated container
```

---

## 3. Key Safety Mechanisms

### 1. The Fanout Freeze (Phase 1)
`tenant-service` broadcasts a `tenant.infrastructure_locking` event over an AMQP fanout exchange. Every `order-service` replica marks the tenant as `MIGRATING` in its local `RoutingRegistry`:
- Incoming write requests for this tenant immediately receive `HTTP 423 Locked`.
- The background outbox worker skips processing staged rows for this tenant.

### 2. Physical Schema Renaming (Phase 2)
Before running the database dump, `infra-provisioner` renames the shared schema:
```sql
ALTER SCHEMA tenant_acme_order_db RENAME TO tenant_acme_order_db_locked;
```
This guarantees that even if a stray request escaped the memory cache lock, PostgreSQL will immediately fail any attempted write rather than accepting a dirty write after the migration dump began.

### 3. Streaming Data Pipe (Phase 3)
Data transfer streams directly between PostgreSQL instances without writing intermediate dump files to disk:
```bash
pg_dump -h shared-db -U postgres -n "tenant_acme_order_db_locked"   | sed 's/tenant_acme_order_db_locked/public/g'   | psql -h dedicated-db -U postgres -d order_db
```

### 4. Cache Clearing and Cutover (Phase 4)
Once migrations are verified on the new container, `tenant-service` broadcasts `tenant.infrastructure_changed`. All service replicas purge their local memory locks and begin routing new transactions to the dedicated database container.

---

## 4. Architectural Invariants & Operational Trade-offs

- **Zero-Lost-Write Invariant**: In-flight transactions are frozen via fanout broadcast locks (`HTTP 423 Locked`) and physical schema renaming before data streaming begins.
- **Outbox Processing Pause**: Background outbox workers skip staged records for tenants in `MIGRATING` status to prevent publishing events during replication.
- **Streaming Pipeline Trade-off**: Streaming directly from shared PostgreSQL to the dedicated container via pipe minimizes disk I/O but requires strict network connectivity between database hosts during migration.
