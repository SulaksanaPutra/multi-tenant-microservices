# Broker API Workspace (Multi-Tenant Microservices Architecture)

This workspace demonstrates a **Multi-Tenant Microservices Architecture** supporting both **Shared (Schema-per-Tenant)** and **Dedicated (Database-per-Tenant via Docker)** isolation models.

---

## 1. System Architecture Overview

```text
+-----------------------------------------------------------------------------------+
|                            System Architecture Overview                           |
+-----------------------------------------------------------------------------------+

[ Client App ]
      │  HTTP Requests (POST /api/register, POST /api/orders, GET /api/orders)
      ▼
[ Traefik Gateway :8000 ]
      │
      ├─────► POST /api/register  ────────► [ tenant-service :8082 ]
      │                                             │ (Outbox Write)
      │                                             ▼
      │                                     [ tenantManagerDB ]
      │                                             │
      │                                             ▼ (Publish)
      │                                     [ RabbitMQ Broker ]
      │                                             │
      │         ┌───────────────────────────────────┼─────────────────────────┬─────────────────────────┐
      │         ▼                                   ▼                         ▼                         ▼
      └─────► POST / GET /api/orders ──► [ order-service :8083 ]    [ user-service :8081 ]   [ notification-service :8084 ]
                                                    │                         │                         │
                            ┌───────────────────────┴───────────┐             ▼                         ▼
                            ▼                                   ▼         [ userDB ]            [ notificationDB ]
                   (Shared Plan: Schema)                 (Dedicated Plan)                        [ Mailpit SMTP ]
                       [ sharedDB ]                      [ Docker Engine ]
                                                                │
                                                                ▼
                                                    [ dedicated_order_db_<id> ]
```

---

## 2. Workflows & Sequences

### 2.1 Registration & Dynamic Infrastructure Provisioning (`POST /api/register`)

```text
+-----------------------------------------------------------------------------------+
|                            POST /api/register Workflow                            |
+-----------------------------------------------------------------------------------+

[ Client ] 
    │  POST /api/register (email, name, tenant_plan)
    ▼
[ tenant-service ] 
    │  1. Save tenant metadata (status: pending)
    │  2. Save event payload to Outbox table
    │     ───► (Inside ONE Database Transaction)
    │  3. Return HTTP 202 Accepted to Client
    ▼
[ Outbox Worker ] 
    │  Reads outbox table & publishes event to queue
    ▼
[ RabbitMQ Queue ] ──► (user.registered)
    │
    ├───────────────────────────────┬──────────────────────────────┐
    ▼                               ▼                              ▼
[ order-service ]               [ user-service ]          [ notification-service ]
    │ Check Inbox table             │ Save user profile        │ Log notification
    │                               ▼                          │ Send welcome email
    ├─► Shared Plan:            [ userDB ]                     ▼
    │   CREATE SCHEMA                                      [ notificationDB ]
    │   order_db_<tenantID>                                [ Mailpit SMTP ]
    │
    ├─► Dedicated Plan:
    │   Spin Postgres Container
    │   dedicated_order_db_<tenantID>
    │
    ▼ Run SQL Migrations
[ Target Order Database ]
    │
    ▼ Directory Write-Back (PATCH /internal/tenants/{tenantID}/infrastructure)
[ tenant-service (tenantManagerDB) ]
```

#### Step-by-Step Breakdown:
1. **User Registration:** Client sends `POST /api/register` specifying `email`, `name`, and `tenant_plan` (`Shared` vs. `Dedicated`).
2. **Atomic Write (Outbox Pattern):** `tenant-service` writes tenant details and an outbox event in **a single database transaction**, returning HTTP `202 Accepted`.
3. **Event Dispatching:** The outbox worker reads the outbox table and publishes `user.registered` to RabbitMQ.
4. **Idempotent Consumption (Inbox Pattern):** `order-service` consumes the event, checking its `inbox` table to skip duplicate executions.
5. **Dynamic Infrastructure Provisioning:**
   - **Shared Plan:** `order-service` creates a PostgreSQL schema (`CREATE SCHEMA order_db_<tenantID>;`).
   - **Dedicated Plan:** `order-service` talks to Docker API to launch a standalone PostgreSQL container (`dedicated_order_db_<tenantID>`).
6. **Directory Write-Back:** `order-service` registers the database DSN back to `tenant-service` (`PATCH /internal/tenants/{tenantID}/infrastructure`).

---

### 2.2 Orders Workflow (`POST /api/orders` & `GET /api/orders`)

```text
+-----------------------------------------------------------------------------------+
|                        POST / GET /api/orders Workflow                            |
+-----------------------------------------------------------------------------------+

[ Client ]
    │  POST /api/orders or GET /api/orders (Header: tenant-x-id)
    ▼
[ order-service ]
    │
    ├────────────► [ Check internal sync.Map cache: map[tenantID]*sql.DB ]
    │                                   │
    │  ┌────────────────────────────────┴────────────────────────────────┐
    │  │                                                                 │
    │  ▼ (Cache Hit - Fast Path)                                         ▼ (Cache Miss - Slow Path)
    │  Use existing *sql.DB pool                                        Call tenant-service HTTP:
    │                                                                   GET /internal/tenants/{tenantID}/infrastructure/order-service
    │                                                                    │
    │                                                                    ▼ Returns DSN
    │                                                                   Open *sql.DB connection pool
    │                                                                   Save pool into sync.Map
    │  ┌─────────────────────────────────────────────────────────────────┘
    │  │
    ▼  ▼
[ Tenant Database (Shared Schema or Dedicated Container) ]
    │ Execute INSERT or SELECT query
    ▼
[ Return Response to Client (201 Created or 200 OK) ]
```

#### Step-by-Step Breakdown:
1. **Request Ingress:** Client sends `POST /api/orders` (create order) or `GET /api/orders` (fetch orders) with `tenant-x-id` in the HTTP header.
2. **Cache Lookup:** `order-service` checks its internal Go `sync.Map` for an active database connection pool.
3. **Fast Path (Cache Hit):** If present, `order-service` executes the query immediately against the tenant's isolated database.
4. **Slow Path (Cache Miss):** If missing, `order-service` calls `tenant-service` (`GET /internal/tenants/{tenantID}/infrastructure/order-service`) to retrieve the DSN, opens a connection pool, caches it in `sync.Map`, and executes the query.

---

## 3. Directory Structure

```text
broker-api/
├── README.md                     # Workspace & Architecture Documentation
├── repomix.config.json           # Repomix code bundle configuration
├── repomix-output.xml            # Compressed repository snapshot
│
├── broker/                       # Infrastructure & Orchestration
│   ├── init.sql                  # Base database initialization scripts
│   └── docker-compose.yml        # Configures Postgres, RabbitMQ, Mailpit, Traefik
│
├── tenant-service/               # Control-Plane Tenant Management & Outbox Service
│   ├── cmd/main.go               # Port 8082 - HTTP API & DSN Directory Service
│   ├── internal/
│   │   ├── repository/           # Tenant metadata & outbox storage
│   │   └── handler/              # Register & internal infrastructure endpoints
│   └── Dockerfile
│
├── user-service/                 # User Identity Service
│   ├── cmd/main.go               # Port 8081 - User Management & Event Consumer
│   └── Dockerfile
│
├── order-service/                # Dynamic Multi-Tenant Data-Plane Service
│   ├── cmd/main.go               # Port 8083 - Orders API & Provisioner Worker
│   ├── internal/
│   │   ├── infrastructure/       # DSN caching & database connection manager
│   │   └── provisioner/          # Schema & Dedicated container provisioner
│   └── Dockerfile
│
├── notification-service/         # Async Notification Worker
│   ├── cmd/main.go               # Port 8084 - Audit Logger & Mailpit Dispatcher
│   └── Dockerfile
│
└── e2e-tests/                    # Automated Integration Tests
    └── register_e2e_test.go      # Dynamic registration & order flow test suite
```

---

## 4. Port Map & Component Dashboard

| Service / Tool | Port | Endpoint / Dashboard | Description |
| :--- | :--- | :--- | :--- |
| **Traefik Gateway** | `8000` | `http://localhost:8000` | Gateway entrypoint for API requests |
| **Traefik Dashboard** | `8080` | `http://localhost:8080` | Route & proxy dashboard |
| **tenant-service** | `8082` | `tenant-service:8082` | Control-plane directory & registration API |
| **order-service** | `8083` | `order-service:8083` | Orders data-plane & provisioner worker |
| **user-service** | `8081` | `user-service:8081` | User profile service |
| **notification-service** | `8084` | `notification-service:8084` | Email notification worker |
| **RabbitMQ Management**| `15672` | `http://localhost:15672` | Queue dashboard (`guest` / `guest`) |
| **Mailpit Dashboard** | `8025` | `http://localhost:8025` | Mock email inbox UI |

---

## 5. How to Run & Stop the Application

### Starting Infrastructure & Microservices

1. **Start Shared Infrastructure (`broker/`)**:
   ```bash
   cd broker && docker compose up -d
   ```
   *Spins up PostgreSQL, RabbitMQ, Mailpit, Traefik Gateway, and creates `broker-network`.*

2. **Start Microservices**:
   Run each service sequentially or in separate terminals:
   ```bash
   # Start Tenant Service (Control Plane & Outbox Worker)
   cd tenant-service && docker compose up -d --build

   # Start User Service
   cd ../user-service && docker compose up -d --build

   # Start Order Service (Data Plane & Dynamic Provisioner)
   cd ../order-service && docker compose up -d --build

   # Start Notification Service (Async Worker)
   cd ../notification-service && docker compose up -d --build
   ```

3. **Run Automated E2E Integration Tests**:
   ```bash
   cd e2e-tests && CGO_ENABLED=0 go test -v ./...
   ```

---

### Stopping All Services

To shut down all microservices and shared infrastructure:

```bash
(cd notification-service && docker compose down) && \
(cd order-service && docker compose down) && \
(cd user-service && docker compose down) && \
(cd tenant-service && docker compose down) && \
(cd broker && docker compose down)
```

---

### Fresh Start / Reset Database Completely

PostgreSQL persists database state in a named Docker volume (`broker_postgres_data`). Standard `docker compose down` leaves the volume intact.

To **wipe the database completely** and start fresh from scratch:

```bash
# 1. Stop all microservices
(cd notification-service && docker compose down) && \
(cd order-service && docker compose down) && \
(cd user-service && docker compose down) && \
(cd tenant-service && docker compose down)

# 2. Wipe infrastructure and persistent volume (-v flag)
cd broker && docker compose down -v

# 3. Start fresh infrastructure (auto-recreates DB & runs init.sql)
cd broker && docker compose up -d
```

