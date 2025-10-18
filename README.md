# Microservice API Workspace (Multi-Tenant Microservices Architecture)

This workspace demonstrates a **Multi-Tenant Microservices Architecture** supporting both **Shared (Schema-per-Tenant)** and **Dedicated (Database-per-Tenant via Docker)** isolation models, powered by an isolated **`infra-provisioner`** pattern, **Declarative Bootstrapping**, and a **Zero-Trust Control Plane** for secure container orchestration and credential protection.

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
      │                                             ▼ (Publish: workspace.initiated)
      │                                     [ RabbitMQ Broker ]
      │                                             │
      │         ┌───────────────────────────────────┼─────────────────────────┬─────────────────────────┐
      │         ▼                                   ▼                         ▼                         ▼
      └─────► POST / GET /api/orders   [ infra-provisioner ]      [ user-service :8081 ]   [ notification-service :8084 ]
                     │                 (Docker Worker, QoS=1)                 │                         │
                     ▼                         │                              ▼                         ▼
            [ order-service :8084 ]            ▼ Publish:               [ userDB ]            [ notificationDB ]
                     │               infrastructure.provisioned                                [ Mailpit SMTP ]
                     ▼ (Runs SQL Migrations)   │
           [ Tenant Database ] ◄───────────────┘
          (Shared or Dedicated)
                     │
                     ▼ Emits: tenant.order_db.ready
            [ tenant-service ] ──► (Passively Activates Workspace & Upserts Routing Metadata)
```

---

## 2. Workflows & Sequences

### 2.1 Registration & Dynamic Infrastructure Provisioning (`POST /api/register`)

```text
+-----------------------------------------------------------------------------------+
|                            POST /api/register Workflow                            |
+-----------------------------------------------------------------------------------+

[ Client ] 
    │  POST /api/register (email, name, plan)
    ▼
[ tenant-service ] 
    │  1. Save tenant metadata (status: pending)
    │  2. Save workspace.initiated event to Outbox table
    │     ───► (Inside ONE Database Transaction)
    │  3. Return HTTP 202 Accepted to Client
    ▼
[ Outbox Worker ] 
    │  Reads outbox table & publishes workspace.initiated event
    ▼
[ RabbitMQ Queue ] ──► (workspace.initiated)
    │
    ├─────────────────────────────────────────────────┐
    ▼                                                 ▼
[ infra-provisioner ]                             [ user-service ]
    │  Prefetch QoS = 1                               │ Create user profile in userDB
    ├─► Shared Plan:                                  │ Emits: user.created
    │   Pass-through metadata                         │
    ├─► Dedicated Plan:                               ▼
    │   Create Docker container                       [ RabbitMQ Queue ]
    │   (512MB RAM, 0.5 CPU limits)                   │
    │   Declaratively bootstrap domain DBs & roles    │
    │   Poll pg_isready health check                  │
    ▼                                                 │
  Publish: infrastructure.provisioned (No Passwords) │
    ▼                                                 │
[ order-service ]                                     │
    │  Derive DB password via ORDER_SERVICE_SECRET    │
    │  Execute SQL migrations (001_create_orders.sql) │
    ▼                                                 │
  Publish: tenant.order_db.ready (Routing Metadata)   │
    ▼                                                 │
[ tenant-service ]                                    │
    │  Passively updates status -> ACTIVE             │
    │  Stores routing metadata (NO PASSWORDS)         │
    │  Emits: workspace.ready                         │
    ▼                                                 │
[ RabbitMQ Queue ]                                    │
    │                                                 │
    └────────────────────────┬────────────────────────┘
                             │ Both events received (Barrier Sync)
                             ▼
                 [ notification-service ]
                             │ Dispatch Welcome Email via Mailpit
```

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
    │  Check PoolRegistry (sync.RWMutex with 15-min TTL)
    ├─────────────────────────────────────────┐
    ▼ (Cache Hit)                             ▼ (Cache Miss)
Use existing *sql.DB pool               GET /internal/tenants/:id/infrastructure/order-service
    │                                   Header: X-Internal-Service-Token
    │                                         │
    │                                         ▼ Returns Routing Metadata (host, port, db_name)
    │                                   Derive ORDER_SERVICE_SECRET password in memory & open pool
    │                                         │
    └───────────────────┬─────────────────────┘
                        ▼
    [ Tenant DB (Shared Schema or Dedicated Container) ]
                        │  Execute Query
                        ▼
    [ Response to Client (201 Created or 200 OK) ]
```

---

## 3. Architecture Deep-Dive Documentation Index

This repository contains comprehensive technical design deep-dives located in the [`docs/`](file:///Users/putubayu/Documents/GitHub/Personal/microservice-api/docs) directory:

| # | Document Title | Focus Area |
| :-: | :--- | :--- |
| 1 | [What Happens If The Broadcaster Breaks?](file:///Users/putubayu/Documents/GitHub/Personal/microservice-api/docs/1-what-happens-if-the-broadcaster-breaks-and-how-do-we-retry.md) | Outbox Pattern, At-Least-Once Delivery & Retry Loops |
| 2 | [How Does Phantom Batch Duplicate Delivery Happen?](file:///Users/putubayu/Documents/GitHub/Personal/microservice-api/docs/2-how-does-the-phantom-batch-duplicate-delivery-happen-and-how-do-we-fix-it.md) | Inbox Pattern, Deduplication Barriers & Consumer Idempotency |
| 3 | [What If The Database Crashes After RabbitMQ Succeeds?](file:///Users/putubayu/Documents/GitHub/Personal/microservice-api/docs/3-what-if-the-database-crashes-after-rabbitmq-succeeds-the-idempotent-consumer.md) | Atomic Transactions & Transactional Inbox Handlers |
| 4 | [What Happens If Tenant Schema Creation Fails Midway?](file:///Users/putubayu/Documents/GitHub/Personal/microservice-api/docs/4-what-happens-if-tenant-schema-creation-fails-midway-transactional-ddl.md) | Transactional DDL, Migration Rollbacks & Schema Safety |
| 5 | [How Do We Scale Multi-Tenancy from Shared Schema to Dedicated Database?](file:///Users/putubayu/Documents/GitHub/Personal/microservice-api/docs/5-how-do-we-scale-multi-tenancy-from-shared-schema-to-dedicated-database-hybrid-duality.md) | Hybrid Multi-Tenant Duality & Plan Upgrades |
| 6 | [How Do We Decouple Control Plane & Data Plane?](file:///Users/putubayu/Documents/GitHub/Personal/microservice-api/docs/6-how-do-we-decouple-control-plane-and-data-plane-workspace-first-b2b-architecture.md) | Passive Control Plane Registry & Zero-Boot Connection Pools |
| 7 | [How Do We Manage Database Transactions and Domain Invariants?](file:///Users/putubayu/Documents/GitHub/Personal/microservice-api/docs/7-how-do-we-manage-database-transactions-and-domain-invariants-outer-layer-unit-of-work.md) | Outer-Layer Unit of Work, Transaction Context & Domain Isolation |
| 8 | [How Do We Isolate Container Orchestration & Prevent Host Takeover?](file:///Users/putubayu/Documents/GitHub/Personal/microservice-api/docs/8-how-do-we-isolate-container-orchestration-and-prevent-host-takeover-infra-provisioner-pattern.md) | `infra-provisioner` Pattern, Docker Socket Isolation & HMAC Credentials |
| 9 | [How Do We Prevent Lateral Movement & Secure the Control Plane?](file:///Users/putubayu/Documents/GitHub/Personal/microservice-api/docs/9-how-do-we-prevent-lateral-movement-and-secure-the-control-plane-zero-trust-metadata-sanitization.md) | Control Plane Metadata Sanitization, Zero-Trust Inter-Service Auth & Ghost Route Removal |
| 10 | [How Do We Isolate Domain Database Secrets Without OCP Violations?](file:///Users/putubayu/Documents/GitHub/Personal/microservice-api/docs/10-how-do-we-isolate-domain-database-secrets-without-ocp-violations-declarative-bootstrapping.md) | Declarative Configuration Bootstrapping, PostgreSQL Role Least Privilege & Root Key Trap Prevention |

---

## 4. Directory Structure

```text
microservice-api/
├── README.md                     # Workspace & Architecture Documentation
│
├── infra-provisioner/            # Isolated Infrastructure Provisioning Worker
│   ├── cmd/main.go               # Entrypoint & RabbitMQ consumer
│   ├── internal/
│   │   ├── crypto/               # HMAC-SHA256 deterministic credential derivation
│   │   ├── docker/               # Docker SDK client with 512MB RAM / 0.5 CPU limits & pg_isready
│   │   └── consumer/             # workspace.initiated consumer (QoS prefetch = 1)
│   └── Dockerfile
│
├── tenant-service/               # Control-Plane Tenant Management & Outbox Service
│   ├── cmd/main.go               # Port 8082 - Control Plane & Outbox Worker
│   ├── internal/
│   │   ├── middleware/           # InternalAuthMiddleware (X-Internal-Service-Token)
│   │   └── repository/           # Control plane metadata-only repository
│   └── Dockerfile
│
├── user-service/                 # User Identity Service
│   ├── cmd/main.go               # Port 8081 - User Profile & Event Consumer
│   └── Dockerfile
│
├── order-service/                # Dynamic Multi-Tenant Data-Plane Service
│   ├── cmd/main.go               # Port 8084 - Orders API & Migration Consumer
│   ├── internal/
│   │   ├── crypto/               # HMAC-SHA256 password derivation
│   │   ├── infrastructure/       # Zero-Trust TenantDB Resolver
│   │   └── registry/             # PoolRegistry with 15-minute TTL eviction
│   └── Dockerfile
│
├── notification-service/         # Async Notification Worker
│   ├── cmd/main.go               # Port 8084 - Mailpit Dispatcher & Audit Logger
│   └── Dockerfile
│
├── infrastructure/               # Shared Infrastructure & Docker Topology
│   ├── init.sql                  # Base database initialization scripts (Sanitized tenant_services schema)
│   ├── docker-compose.yml        # Postgres, RabbitMQ, Mailpit, Traefik, Infra-Provisioner, Web-UI
│   └── web-ui/                   # Functional Web UI
│
├── docs/                         # Architectural Deep-Dives & Technical Design Challenges
│   └── 10-how-do-we-isolate-domain-database-secrets...md
│
└── e2e-tests/                    # Automated Integration Tests
    └── register_e2e_test.go      # Dynamic registration & order flow test suite
```

---

## 5. Port Map & Component Dashboard

| Service / Tool | Port | Endpoint / Dashboard | Description |
| :--- | :--- | :--- | :--- |
| **Traefik Gateway** | `8000` | `http://localhost:8000` | Gateway entrypoint for APIs & Web UI |
| **Flow Demo Web UI** | `8000` | `http://localhost:8000/` | Web UI for tenant registration & orders |
| **tenant-service** | `8082` | `tenant-service:8082` | Control plane registry & registration API |
| **order-service** | `8084` | `order-service:8084` | Orders data plane & migration consumer |
| **user-service** | `8081` | `user-service:8081` | User profile service |
| **notification-service** | `8084` | `notification-service:8084` | Email notification worker |
| **infra-provisioner** | *None* | *Internal Worker* | Docker container provisioner (QoS=1, isolated socket) |
| **RabbitMQ Management**| `15672` | `http://localhost:15672` | Queue dashboard (`guest` / `guest`) |
| **Mailpit Dashboard** | `8025` | `http://localhost:8025` | Mock email inbox UI |

---

## 6. How to Run & Stop the Application

### Starting Infrastructure & Services

```bash
# 1. Start Shared Infrastructure & Infra Provisioner
(cd infrastructure && docker compose up -d)

# 2. Start Microservices
(cd tenant-service && docker compose up -d --build) && \
(cd user-service && docker compose up -d --build) && \
(cd order-service && docker compose up -d --build) && \
(cd notification-service && docker compose up -d --build)

# 3. Run E2E Integration Tests
(cd e2e-tests && CGO_ENABLED=0 go test -v ./...)
```

### Stopping All Services

```bash
(cd notification-service && docker compose down) && \
(cd order-service && docker compose down) && \
(cd user-service && docker compose down) && \
(cd tenant-service && docker compose down) && \
(cd infrastructure && docker compose down)
```
