# Microservice API Workspace (Multi-Tenant Microservices Architecture)

This workspace demonstrates a **Multi-Tenant Microservices Architecture** supporting both **Shared (Schema-per-Tenant)** and **Dedicated (Database-per-Tenant via Docker)** isolation models, powered by an isolated **`infra-provisioner`** pattern, **Declarative Bootstrapping**, and a **Zero-Trust Control Plane** for secure container orchestration and credential protection.

---

## 1. System Architecture Overview

```text
+-----------------------------------------------------------------------------------+
|                            System Architecture Overview                           |
+-----------------------------------------------------------------------------------+

[ Client App ]
      │  HTTP Requests (POST /auth/login, POST /api/register, POST/GET /api/orders)
      ▼
[ Traefik Gateway :8000 ]
      │
      ├─────► POST /auth/login ───────────► [ auth-service :8085 ]
      │       POST /auth/refresh                    │
      │                                             ▼
      │                                        [ authDB ] (Bcrypt & Refresh Tokens)
      │
      ├─────► POST /api/register ─────────► [ tenant-service :8082 ]
      │                                             │ (Outbox Write)
      │                                             ▼
      │                                     [ tenantManagerDB ]
      │                                             │
      │                                             ▼ (Publish: workspace.initiated)
      │                                     [ RabbitMQ Broker ]
      │                                             │
      │         ┌───────────────────────────────────┼─────────────────────────┬─────────────────────────┐
      │         ▼                                   ▼                         ▼                         ▼
      └─────► POST / GET /api/orders   [ infra-provisioner ]      [ user-service :8081 ]   [ notification-service :8083 ]
               (Bearer <JWT>)          (Docker Worker, QoS=1)                 │               (Bearer <JWT>)
                     │                         │                              ▼                         │
                     ▼                         │                          [ userDB ]                    ▼
            [ order-service :8084 ]            ▼ Publish:                                         [ notificationDB ]
              (RS256 JWT Verification) infrastructure.provisioned                                  [ Mailpit SMTP ]
                     │                         │
                     ▼ (Runs SQL Migrations)   │
           [ Tenant Database ] ◄───────────────┘
          (Shared or Dedicated)
                     │
                     ▼ Emits: tenant.order_db.ready
            [ tenant-service ] ──► (Passively Activates Workspace & Upserts Routing Metadata)
```

---

## 2. Workflows & Sequences

### 2.1 Microservice Boot & Domain Permission Registration (`POST /internal/permissions/register`)

```text
+-----------------------------------------------------------------------------------+
|               Microservice Boot & Domain Permission Registration                  |
+-----------------------------------------------------------------------------------+

[ order-service Boot ]       [ notification-service Boot ]     [ user-service Boot ]
          │                               │                           │
          │ POST /internal/permissions    │ POST /internal/permission │ POST /internal/permissions
          │ (X-Internal-Service-Token)    │ (X-Internal-Service-Token)│ (X-Internal-Service-Token)
          ▼                               ▼                           ▼
  ┌───────────────────────────────────────────────────────────────────────────┐
  │                         [ auth-service :8085 ]                            │
  │  1. Receives domain permission declarations                             │
  │  2. Executes idempotent upsert: ON CONFLICT (name) DO UPDATE              │
  │  3. Non-blocking HTTP semaphore prevents startup stampedes               │
  └─────────────────────────────────────┬─────────────────────────────────────┘
                                        ▼
                            [ auth_db.permissions ]
                     (Centralized Opaque Permission Store)
```

* **Domain-Driven Permission Ownership:** Domain services (`order-service`, `notification-service`, `user-service`) own their atomic capability strings (e.g. `orders:create`, `orders:read`).
* **Non-Blocking Registration:** Services register capabilities at startup via an internal HTTP semaphore contract (`POST /internal/permissions/register`). `auth-service` persists them as opaque strings without needing compile-time knowledge of domain semantics.

---

### 2.2 Open Registration & Dynamic Infrastructure Provisioning (`POST /api/register`)

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
  Publish: infrastructure.provisioned (No Passwords)  │
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
[ RabbitMQ Queue ] ───────────────────────────────────┘
```

---

### 2.3 Password Setup & Credential Initialization (`POST /auth/credentials/setup`)

```text
+-----------------------------------------------------------------------------------+
|                   Password Setup & Credential Initialization Workflow             |
+-----------------------------------------------------------------------------------+

[ RabbitMQ Queue ]
    │  Both events received: workspace.ready + user.created (Barrier Sync)
    ▼
[ notification-service ]
    │  1. Barrier Sync met (workspace.ready + user.created)
    │  2. Synchronous fetch: POST /internal/auth/setup-token (Header: X-Internal-Service-Token)
    ▼
[ auth-service ]
    │  1. Generates 256-bit single-use setup token in RAM
    │  2. Saves SHA-256(raw_token) in auth_db.password_setup_tokens
    │  3. Seeds default system roles (admin, viewer) for new tenant_id
    │  4. Assigns user_id the 'admin' role in auth_db.user_roles
    │  5. Returns raw setup token in HTTP 200 OK
    ▼
[ notification-service ]
    │  Dispatches Welcome Email with Setup Link via Mailpit:
    │  http://localhost:8000/setup-password?token=<RAW_TOKEN>
    ▼
[ Client / User ]
    │  POST /auth/credentials/setup { "token": "<RAW_TOKEN>", "password": "..." }
    ▼
[ auth-service ]
    │  1. Validates setup token hash, expiry & unused state
    │  2. Hashes password with bcrypt & upserts into auth_db.user_credentials
    │  3. Marks setup token as USED in auth_db.password_setup_tokens
    │  4. Mints RS256 JWT Access Token (Claims: userID, tenantID, email, permissions[], perm_version)
    │  5. Returns { "access_token": "<JWT>", "refresh_token": "<raw>" } to Client
```

---

### 2.4 User Authentication & RS256 JWT Access Token Flow (`POST /auth/login`)

```text
+-----------------------------------------------------------------------------------+
|                        User Login & Token Issuance Flow                           |
+-----------------------------------------------------------------------------------+

[ Client ] ─────► POST /auth/login { "email": "...", "password": "..." }
                        │
                        ▼
                [ auth-service :8085 ]
                        │  1. Verify bcrypt password in auth_db.user_credentials
                        │  2. Query assigned permissions & perm_version:
                        │     user_roles ──► roles ──► role_permissions ──► permissions
                        │  3. Sign RS256 Access Token:
                        │     Claims: { sub, tenant_id, email, permissions: [...], perm_version: N }
                        │  4. Save SHA-256(raw_refresh_token) to auth_db.refresh_tokens (7d TTL)
                        │
                        ▼
[ Client ] ◄───── Return { "access_token": "<JWT>", "refresh_token": "<raw>" }
```

---

### 2.5 Domain Service Role & Permission Management (`/api/users/roles` & `/api/users/:user_id/role`)

```text
+-----------------------------------------------------------------------------------+
|      Clean Architecture Domain Service Role & Permission Management Flow          |
+-----------------------------------------------------------------------------------+

[ Client / Tenant Admin ]
          │  POST /api/users/roles or PUT /api/users/:user_id/role (Bearer <JWT>)
          ▼
  [ user-service :8081 ]
          │  1. UserHandler (Layer 1 Entry Point): Parses request & Bearer token
          │  2. Calls Layer 2: userService.AssignUserRole(ctx, authToken, userID, roleID)
          │  3. UserService (Layer 2 Core): Validates domain invariants & calls RoleClient
          │  4. AuthClient (Layer 3 Outbound Adapter): Issues HTTP request to auth-service:8085
          ▼
  [ auth-service :8085 ]
          │  1. RequirePermission("users:roles:manage") middleware check
          │  2. Enforces tenant-scoped role uniqueness: UNIQUE (tenant_id, name)
          │  3. Protects system roles (is_system = true) from mutation
          │  4. Atomically batch-increments user_permission_versions.version for assigned users
          ▼
  [ auth_db.roles / role_permissions / user_roles / user_permission_versions ]
```

* **Strict Clean Architecture Layering:** `UserHandler` (Layer 1) maintains a single dependency on `UserService` (Layer 2). `UserService` consumes the `RoleClient` outbound port interface, implemented by `authclient.AuthClient` (Layer 3 Driven Adapter).
* **Tenant-Scoped Custom Roles:** Tenant admins compose custom roles using domain-registered permissions. System default roles (`admin`, `viewer`) are seeded automatically and protected from mutation (`is_system = true`).
* **Near-Instant Permission Revocation:** Updating role permissions increments `user_permission_versions.version` in a single batch, invalidating cached JWT permissions across downstream microservices.

---

### 2.6 Orders API: Authorization Enforcement & Dynamic DSN Resolution (`POST / GET /api/orders`)

```text
+-----------------------------------------------------------------------------------+
|          POST / GET /api/orders Workflow (RBAC & Dynamic DSN Resolution)          |
+-----------------------------------------------------------------------------------+

[ Client ] ─────► POST /api/orders or GET /api/orders (Header: Authorization: Bearer <JWT>)
                        │
                        ▼
                [ order-service :8084 ]
                        │
                        ├─► 1. RequireJWT Middleware: In-memory RS256 signature check (0 network calls)
                        ├─► 2. RequirePermission("orders:create"): Checks "orders:create" in claims.permissions[]
                        │
                        ├─► 3. Permission Version Check (Local VersionCache):
                        │      ├─► Fast Path (Cache Hit & claims.perm_version == cached_version): Proceed (< 100ns)
                        │      └─► Slow Path (Cache Miss / Mismatch):
                        │            GET /internal/auth/users/:userID/perm-version (auth-service :8085)
                        │            Header: X-Internal-Service-Token
                        │            If fetched_version > claims.perm_version -> Reject 401 Token Superseded
                        │
                        ├─► 4. Dynamic Tenant DSN Resolution (TenantDBResolver):
                        │      ├─► Fast Path: Local RoutingRegistry materialized view hit
                        │      └─► Slow Path (Cache Miss):
                        │            GET /internal/tenants/:id/infrastructure/order-service (tenant-service :8082)
                        │            Header: X-Internal-Service-Token
                        │            Derive ORDER_SERVICE_SECRET password in memory & open pool
                        │
                        ▼
    [ Tenant Database (Shared Schema or Dedicated Container) ]
                        │
                        ▼ Execute Query: WHERE tenant_id = jwt.tenant_id
                        │
    [ Response to Client (201 Created or 200 OK) ]
```

---

### 2.7 Advanced Infrastructure Availability & Cache Invalidation (`tenant.infrastructure_changed`)

```text
+-----------------------------------------------------------------------------------+
|      Infrastructure Availability, Container Rebinding & Cache Invalidation        |
+-----------------------------------------------------------------------------------+

[ Container Event / Admin / Failover ]
    │  Container Rescheduled, IP/Port Changed, or Plan Upgraded/Downgraded
    ▼
[ tenant-service ]
    │  1. Updates tenant infrastructure metadata (host, port, isolation mode)
    │  2. Emits tenant.infrastructure_changed event to Topic Exchange
    ▼
[ RabbitMQ Topic Exchange (company.events) ]
    │
    ├───────────────────────────────┬───────────────────────────────┐
    ▼ (Broadcast Key: tenant.infra_changed)                         ▼
[ Exclusive Queue: amq.gen-1 ]  [ Exclusive Queue: amq.gen-2 ]  [ Exclusive Queue: amq.gen-3 ]
    │                               │                               │
    ▼                               ▼                               ▼
[ order-service-replica-1 ]     [ order-service-replica-2 ]     [ order-service-replica-3 ]
    │ (Purges local Routing &       │ (Purges local Routing &       │ (Purges local Routing &
    │  PoolRegistry caches)         │  PoolRegistry caches)         │  PoolRegistry caches)
    ▼                               ▼                               ▼
 Next request fetches fresh      Next request fetches fresh      Next request fetches fresh
 Rebound Database DSN            Rebound Database DSN            Rebound Database DSN
```

* **New Tenant Registration:** Every `order-service` replica experiences a natural cache miss on its first request and lazily resolves the routing metadata.
* **Infrastructure Rebinding & Plan Changes:** If a dedicated DB container dies and is rescheduled on a new IP/port by Docker/K8s, or if a tenant undergoes a plan upgrade/downgrade, `tenant-service` broadcasts `tenant.infrastructure_changed` over the **`company.events` Topic Exchange** to exclusive anonymous queues, forcing all `order-service` replicas to purge their local `RoutingRegistry` and `PoolRegistry` connection caches in real-time.

---

### 2.8 Unified Identity & Workspace Selection (`POST /api/auth/login` + `POST /api/auth/select-tenant`)

**Data Model (Slack / GitHub / Vercel Paradigm):**

```text
 user_db.public.users                  auth_db.public.user_credentials        auth_db.public.user_tenant_memberships
 ┌──────────────────────────┐          ┌───────────────────────────────────┐  ┌─────────────────────────────────────────┐
 │ id          PK           │          │ user_id         PK                │  │ user_id    FK ────────────────────┐     │
 │ email       UNIQUE       │          │ email           UNIQUE (1/email)  │  │ tenant_id                         │     │
 │ name                     │          │ password_hash   (bcrypt)          │  │ created_at                        │     │
 │ created_at / updated_at  │          │ created_at / updated_at           │  │ PRIMARY KEY (user_id, tenant_id)  │     │
 └──────────────────────────┘          └───────────────────────────────────┘  └─────────────────────────────────────────┘
                                           │  NO tenant_id column!              │
                                           │  (global identity,                 │
                                           │   not tenant-scoped)               │
                                           ▼                                    ▼
                                  Credential.TenantID = ""              resolved at ISSUANCE time
                                  when read from DB                     via GetUserMemberships()
```

**Login flow — workspace selection (single or multiple):**

```text
[ Client ] ────► POST /api/auth/login { "email", "password" }
                      │
                      ▼
              [ auth-service ]
                      │ 1. FindByEmail()      -> 1 global credential row (tenant_id = "" << intentional)
                      │ 2. bcrypt.CompareHashAndPassword()  -- fail? -> 401 Invalid Credentials
                      │ 3. GetUserMemberships(user_id)
                      │      SELECT tenant_id FROM user_tenant_memberships WHERE user_id = $1
                      │
                      ├──────────────────────┬──────────────────────
                      ▼                      ▼
              0 memberships           1+ memberships
                      │                      │
                      ▼                      ▼
                401 No Tenant        create single-use EXCHANGE TOKEN
                  Membership          (10 min TTL, saved in password_setup_tokens)
                                         │
                                         ▼
return { status: "SELECT_WORKSPACE",
                                   exchange_token,
                                   workspaces: [ {tenant_id, tenant_name, tenant_slug, tenant_plan}, ... ] }
                                         │
                                         ▼
                             [ Client ] ──► POST /api/auth/select-tenant
                                    { exchange_token, tenant_id }
                                         │
                                         ▼
                                [ auth-service ]
                                  • validate exchange token (hash / used / expiry)
                                  • verify tenant_id ∈ GetUserMemberships(user_id)
                                  • MarkTokenUsed()
                                  • issuePair(tenant)
                                         │
                                         ▼
                                return { access_token,
                                  refresh_token (bound to tenant) }
```

The client completes the flow: with **one** workspace it exchanges the token silently; with **multiple** it presents a workspace-selection modal. The workspace list is enriched server-side by `auth-service` (best-effort, zero-trust `GET /internal/tenants/:id/profile` on tenant-service) with `tenant_name`/`tenant_slug`/`tenant_plan`, with a fallback to raw `tenant_id` if the lookup fails.

**Why `user_credentials` has no `tenant_id` (intentional, not a bug):**

* `user_credentials` is a **global identity row** (1 per email); the `tenant_id` column was deliberately removed in the Unified Identity migration.
* Tenants live in `user_tenant_memberships`; the credential row itself is tenant-agnostic.
* `Credential.TenantID` is a **working value set at issuance time**, never persisted. The source of truth is `GetUserMemberships()`.
* Every issuance path must resolve the tenant before `issuePair()`: SelectWorkspace (explicit `tenant_id`, re-verified against memberships), RefreshToken (reads `refresh_tokens.tenant_id` bound at issuance), and SetupPassword. `Login` never issues directly — it always returns an exchange token for the client to exchange via `select-tenant`.
* ⚠️ **Footgun:** any path that issues a token without resolving the tenant mints a tenant-less JWT — this was the refresh bug, now fixed by binding `tenant_id` onto the refresh-token row and by making `issuePair` take the tenant as an explicit parameter.

---

## 3. Microservice Layer Hierarchy & Documentation Topology

This workspace enforces strict **Clean Architecture boundaries** across all microservices. The documentation follows a **2-Tier Macro/Micro Model**:

1. **Macro System Mesh (Root Documentation):** Focuses on global orchestration, cross-cutting distributed workflows, security boundaries, and AMQP contracts (see Sections 1, 2 & 4).
2. **Clean Architecture Standards ([docs/00-clean-architecture-standards-and-layer-hierarchy.md](docs/00-clean-architecture-standards-and-layer-hierarchy.md)):** Comprehensive documentation of Layer 1 (Adapters), Layer 2 (Service Core), Layer 3 (Persistence), transaction ownership rules (`txManager.WithTransaction`), and flow diagrams.
3. **Micro Domain Services (Service READMEs):** Each microservice maintains its local domain contracts, archetype declaration, and local exception rationale:
   - [`order-service/README.md`](order-service/README.md) - Archetype A: Dynamic DSN resolution, PoolRegistry & `MigrationService` exemption.
   - [`notification-service/README.md`](notification-service/README.md) - Archetype B: Barrier Sync pattern & Mailpit SMTP delivery outside tx.
   - [`tenant-service/README.md`](tenant-service/README.md) - Archetype A: Control-plane registry, Outbox worker & infrastructure routing update.
   - [`auth-service/README.md`](auth-service/README.md) - Archetype A: RS256 JWT key pair, refresh token hashing & permissions registration.
   - [`infra-provisioner/README.md`](infra-provisioner/README.md) - Archetype C: Isolated Docker container provisioner & QoS=1 AMQP worker.
   - [`user-service/README.md`](user-service/README.md) - Archetype A: Identity profile management & `workspace.initiated` event listener.

---

## 4. Architecture Deep-Dive Documentation Index

This repository contains comprehensive technical design deep-dives located in the [`docs/`](docs/) directory:

| # | Document Title | Focus Area |
| :-: | :--- | :--- |
| 0 | [Clean Architecture Standards & Layer Hierarchy](docs/00-clean-architecture-standards-and-layer-hierarchy.md) | 3-Layer Mental Model, Transaction Boundaries & Delivery Flow Diagrams |
| 1 | [What Happens If The Broadcaster Breaks?](docs/1-what-happens-if-the-broadcaster-breaks-and-how-do-we-retry.md) | Outbox Pattern, At-Least-Once Delivery & Retry Loops |
| 2 | [How Does Phantom Batch Duplicate Delivery Happen?](docs/2-how-does-the-phantom-batch-duplicate-delivery-happen-and-how-do-we-fix-it.md) | Inbox Pattern, Deduplication Barriers & Consumer Idempotency |
| 3 | [What If The Database Crashes After RabbitMQ Succeeds?](docs/3-what-if-the-database-crashes-after-rabbitmq-succeeds-the-idempotent-consumer.md) | Atomic Transactions & Transactional Inbox Handlers |
| 4 | [What Happens If Tenant Schema Creation Fails Midway?](docs/4-what-happens-if-tenant-schema-creation-fails-midway-transactional-ddl.md) | Transactional DDL, Migration Rollbacks & Schema Safety |
| 5 | [How Do We Scale Multi-Tenancy from Shared Schema to Dedicated Database?](docs/5-how-do-we-scale-multi-tenancy-from-shared-schema-to-dedicated-database-hybrid-duality.md) | Hybrid Multi-Tenant Duality & Plan Upgrades |
| 6 | [How Do We Decouple Control Plane & Data Plane?](docs/6-how-do-we-decouple-control-plane-and-data-plane-workspace-first-b2b-architecture.md) | Passive Control Plane Registry & Zero-Boot Connection Pools |
| 7 | [How Do We Manage Database Transactions and Domain Invariants?](docs/7-how-do-we-manage-database-transactions-and-domain-invariants-outer-layer-unit-of-work.md) | Outer-Layer Unit of Work, Transaction Context & Domain Isolation |
| 8 | [How Do We Isolate Container Orchestration & Prevent Host Takeover?](docs/8-how-do-we-isolate-container-orchestration-and-prevent-host-takeover-infra-provisioner-pattern.md) | `infra-provisioner` Pattern, Docker Socket Isolation & HMAC Credentials |
| 9 | [How Do We Prevent Lateral Movement & Secure the Control Plane?](docs/9-how-do-we-prevent-lateral-movement-and-secure-the-control-plane-zero-trust-metadata-sanitization.md) | Control Plane Metadata Sanitization, Zero-Trust Inter-Service Auth & Ghost Route Removal |
| 10 | [How Do We Isolate Domain Database Secrets Without OCP Violations?](docs/10-how-do-we-isolate-domain-database-secrets-without-ocp-violations-declarative-bootstrapping.md) | Declarative Configuration Bootstrapping, PostgreSQL Role Least Privilege & Root Key Trap Prevention |
| 11 | [How Do We Eliminate Cache Stampedes and Decouple Data Plane Routing?](docs/11-how-do-we-eliminate-cache-stampedes-and-decouple-data-plane-routing-singleflight-and-in-memory-materialized-view.md) | Request Coalescing (`singleflight`), Context Shielding & Local In-Memory `RoutingRegistry` Materialized View |
| 12 | [How Do We Prevent PostgreSQL Transaction Abortion?](docs/12-how-do-we-prevent-postgresql-transaction-abortion-and-maintain-clean-outer-layer-unit-of-work.md) | Outer-Layer Consumer Unit-of-Work, PostgreSQL Aborted Transaction Trap (`23505`) & `ON CONFLICT DO NOTHING` |
| 13 | [How Do We Prevent Horizontal Split-Brain Cache Invalidation and Socket Sprawl?](docs/13-how-do-we-prevent-horizontal-split-brain-cache-invalidation-and-multi-tenant-connection-sprawl.md) | Fanout Broadcast Topology, Competing Consumer DDL Migration Guardrail, Double-Checked Reaper Sweeps & Connection Pool Tuning |
| 14 | [How Do We Prevent Transient Network Split-Brain from AMQP Cache Invalidation Loss?](docs/14-how-do-we-prevent-transient-network-split-brain-cache-invalidation-loss.md) | Two-Layer Reconnect Driver, `NotifyReconnect` Signal, `PurgeAll` Cache Barriers & Re-Binding Topology |
| 15 | [When the Broker Goes Silent, Does Your Service Tell the Truth?](docs/15-when-the-broker-goes-silent-does-your-service-tell-the-truth.md) | Context Lifecycles, Cache Miss Throttling, Singleflight & Load Shedding |
| 16 | [How Do We Safely Manage Multi-Tenant Database Duality Without Data Leakage or Connection Sprawl?](docs/16-how-do-we-safely-manage-multi-tenant-database-duality-without-cross-tenant-data-leakage-or-connection-sprawl.md) | Constructor Arity, Dynamic DSN Resolution, Bounded LRU Connection Pooling & Clean Architecture Isolation |
| 17 | [How Do We Isolate Authentication and Token Issuance in a Standalone Microservice?](docs/17-how-do-we-isolate-authentication-and-jwt-token-issuance-standalone-auth-service.md) | RS256 Asymmetric Key Verification, Opaque Refresh Token Rotation & Stage 1 Scaffolding Scope |
| 18 | [How Do We Design Multi-Tenant RBAC with Domain-Distributed Permission Ownership?](docs/18-how-do-we-design-multi-tenant-rbac-with-domain-distributed-permission-ownership.md) | Hybrid Centralized Permission Registry, Tenant-Scoped Custom Roles, JWT Claim Enrichment & Startup Registration |
| 19 | [How Do We Achieve Instant Revocation in Stateless RS256 JWTs via Version Caching?](docs/19-how-do-we-achieve-instant-jwt-revocation-with-perm-version-caching.md) | Stateless JWT Claims, Token Bloat Math, `perm_version` Claim & Local In-Memory VersionCache Enforcement |
| 20 | [How Do We Design User-Tenant Session Binding and Zero-Trust Token Context Derivation?](docs/20-how-do-we-design-user-tenant-session-binding-and-zero-trust-token-context-derivation.md) | 1-to-1 Active Session Claims, Eliminating Client-Side tenant_id Exposure, IDOR Protection & Future Multi-Workspace Switching |
| 21 | [How Do We Implement Unified Identity and Workspace Selection?](docs/21-how-do-we-implement-unified-identity-and-workspace-selection.md) | Global Identity per Email, `user_tenant_memberships`, Exchange-Token Workspace Selection & Tenant-Bound Refresh Tokens |

---

## 5. Directory Structure

```text
microservice-api/
├── README.md                     # Workspace & Architecture Documentation
│
├── auth-service/                 # Central Authentication, Identity & RBAC Service
│   ├── cmd/main.go               # Port 8085 - RS256 JWT Issuer, Credentials Setup & Permission Registry
│   ├── internal/
│   │   ├── crypto/               # RS256 signing, verification & JWKS builder
│   │   ├── handler/              # Auth, Setup, Permission & Role CRUD Handlers
│   │   ├── repository/           # Credentials, Refresh Tokens, Permissions & Roles Repositories
│   │   └── service/              # Login, Setup Token, Permission & Role Management Core
│   └── Dockerfile
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
│   │   ├── infrastructure/       # AuthClient PermissionRegistrar (tenants:read/update/plan.change)
│   │   ├── middleware/           # InternalAuthMiddleware & RequirePermission RBAC
│   │   └── repository/           # Control plane metadata-only repository
│   └── Dockerfile
│
├── user-service/                 # User Identity Service
│   ├── cmd/main.go               # Port 8081 - User Profile & Event Consumer
│   ├── internal/
│   │   ├── service/              # Layer 2 Core (UserService & RoleClient port interface)
│   │   ├── infrastructure/       # Layer 3 AuthClient Driven Adapter & PermissionRegistrar
│   │   └── middleware/           # RequireJWT & RequirePermission RBAC
│   └── Dockerfile
│
├── order-service/                # Dynamic Multi-Tenant Data-Plane Service
│   ├── cmd/main.go               # Port 8084 - Orders API & Migration Consumer
│   ├── internal/
│   │   ├── crypto/               # HMAC-SHA256 password derivation
│   │   ├── infrastructure/       # Zero-Trust TenantDB Resolver & AuthClient PermissionRegistrar
│   │   ├── middleware/           # RequireJWT & RequirePermission RBAC (orders:create/read)
│   │   └── registry/             # PoolRegistry with 15-minute TTL eviction
│   └── Dockerfile
│
├── notification-service/         # Async Notification Worker & Internal Setup Client
│   ├── cmd/main.go               # Port 8083 - Mailpit Dispatcher & Setup Token Client
│   ├── internal/
│   │   ├── infrastructure/       # AuthClient PermissionRegistrar (notifications:read)
│   │   └── middleware/           # RequireJWT & RequirePermission RBAC
│   └── Dockerfile
│
├── infrastructure/               # Shared Infrastructure & Docker Topology
│   ├── init.sql                  # Base database initialization scripts (Sanitized auth_db & tenant_services schemas)
│   ├── docker-compose.yml        # Postgres, RabbitMQ, Mailpit, Traefik, Infra-Provisioner, Web-UI
│   └── web-ui/                   # Functional Web UI
│
├── docs/                         # Architectural Deep-Dives & Technical Design Challenges (Docs 0 - 19)
│   └── 19-how-do-we-achieve-instant-jwt-revocation...md
│
└── e2e-tests/                    # Automated Integration Tests
    └── register_e2e_test.go      # Dynamic registration & order flow test suite
```

---

## 6. Port Map & Component Dashboard

| Service / Tool | Port | Endpoint / Dashboard | Description |
| :--- | :--- | :--- | :--- |
| **Traefik Gateway** | `8000` | `http://localhost:8000` | Gateway entrypoint for APIs & Web UI |
| **Flow Demo Web UI** | `8000` | `http://localhost:8000/` | Web UI for tenant registration & orders |
| **tenant-service** | `8082` | `tenant-service:8082` | Control plane registry & registration API |
| **order-service** | `8084` | `order-service:8084` | Orders data plane & migration consumer |
| **user-service** | `8081` | `user-service:8081` | User profile service |
| **auth-service** | `8085` | `http://localhost:8085` | RS256 JWT token issuer & authentication service |
| **notification-service** | `8083` | `notification-service:8083` | Email notification worker |
| **infra-provisioner** | *None* | *Internal Worker* | Docker container provisioner (QoS=1, isolated socket) |
| **RabbitMQ Management**| `15672` | `http://localhost:15672` | Queue dashboard (`guest` / `guest`) |
| **Mailpit Dashboard** | `8025` | `http://localhost:8025` | Mock email inbox UI |

---

## 7. API Reference Specification

### 7.1 Client-Facing / Public Gateway APIs (Traefik Ingress `:8000`)

| Service | Method & Path | Auth / Headers | Required Scope / Permission | Description |
| :--- | :--- | :--- | :--- | :--- |
| **tenant-service** | `POST /api/tenants/register` | None | Public | Register new workspace & initiate provisioner workflow |
| **tenant-service** | `GET /api/tenants/me` | Bearer JWT | `tenants:read` | Retrieve authenticated tenant workspace profile metadata |
| **tenant-service** | `PUT /api/tenants/me` | Bearer JWT | `tenants:write` | Update tenant metadata (name, slug, owner info) |
| **tenant-service** | `PUT /api/tenants/me/plan` | Bearer JWT | `tenants:write` | Upgrade/downgrade tenant isolation plan (`shared` / `dedicated`) |
| **auth-service** | `GET /.well-known/jwks.json` | None | Public | Public RSA key set for RS256 JWT signature verification |
| **auth-service** | `POST /api/auth/credentials/setup` | None | Public | Setup user password via setup token |
| **auth-service** | `POST /api/auth/login` | None | Public | User authentication & RS256 JWT access token issuance |
| **auth-service** | `POST /api/auth/refresh` | None | Public | Refresh expired access tokens |
| **auth-service** | `POST /api/auth/logout` | Bearer JWT | Authenticated | Revoke refresh token |
| **auth-service** | `GET /api/auth/permissions` | Bearer JWT | Tenant Admin | List catalog of registered system permissions |
| **auth-service** | `POST /api/auth/roles` | Bearer JWT | Tenant Admin | Create tenant-scoped custom role |
| **auth-service** | `GET /api/auth/roles` | Bearer JWT | Tenant Admin | List available roles for tenant |
| **auth-service** | `GET /api/auth/roles/:id` | Bearer JWT | Tenant Admin | Retrieve specific role details |
| **auth-service** | `PUT /api/auth/roles/:id/permissions` | Bearer JWT | Tenant Admin | Update permissions linked to role |
| **auth-service** | `DELETE /api/auth/roles/:id` | Bearer JWT | Tenant Admin | Delete custom role |
| **auth-service** | `PUT /api/auth/users/:userID/role` | Bearer JWT | Tenant Admin | Assign role to tenant user |
| **auth-service** | `GET /api/auth/users/:userID/role` | Bearer JWT | Tenant Admin | Retrieve user role assignment |
| **user-service** | `GET /api/users` | Bearer JWT | `users:read` | List users belonging to caller's tenant |
| **user-service** | `GET /api/users/me` | Bearer JWT | `users:read` | Retrieve current user profile |
| **user-service** | `PUT /api/users/me` | Bearer JWT | `users:write` | Update current user profile details |
| **user-service** | `GET /api/users/:user_id/role` | Bearer JWT | `users:read` | Retrieve user role assignment |
| **user-service** | `PUT /api/users/:user_id/role` | Bearer JWT | `users:roles:manage` | Assign role to user |
| **user-service** | `POST /api/users/roles` | Bearer JWT | `users:roles:manage` | Create tenant-scoped custom role using system permissions |
| **user-service** | `GET /api/users/roles` | Bearer JWT | `users:roles:manage` | List available roles for tenant |
| **order-service** | `GET /api/orders` | Bearer JWT | `orders:read` | List orders for isolated tenant DB |
| **order-service** | `POST /api/orders` | Bearer JWT | `orders:create` | Create order entry in isolated tenant DB |
| **notification-service** | `GET /api/notifications` | Bearer JWT | `notifications:read` | List user notifications |

---

### 7.2 Internal Control-Plane APIs (Zero-Trust Inter-Service Auth)

| Service | Method & Path | Required Header | Description |
| :--- | :--- | :--- | :--- |
| **auth-service** | `POST /internal/auth/setup-token` | `X-Internal-Service-Token` | Generate 256-bit single-use password setup token |
| **auth-service** | `POST /internal/auth/permissions/register` | `X-Internal-Service-Token` | Bootstrapping endpoint for domain permission registration |
| **auth-service** | `GET /internal/auth/users/:userID/perm-version` | `X-Internal-Service-Token` | Fetch user permission version for cache invalidation |
| **tenant-service** | `GET /internal/tenants/:id/infrastructure/:service` | `X-Internal-Service-Token` | Query tenant database infrastructure & routing metadata |
| **tenant-service** | `GET /internal/tenants/:id/profile` | `X-Internal-Service-Token` | Query tenant control-plane metadata (name/slug/plan) for auth-service workspace enrichment |

---

### 7.3 Health & Diagnostic Endpoints

| Service | Method & Path | Auth | Description |
| :--- | :--- | :--- | :--- |
| **All Microservices** | `GET /health` | None | HTTP 200 OK liveness check |

---

## 8. How to Run & Stop the Application

### Starting Infrastructure & Services

```bash
# 1. Start Shared Infrastructure, Infra Provisioner & Web UI
#    Note: web-ui embeds its static assets (public/*) into the Go binary at build
#    time via //go:embed, so use --build whenever you edit any public/* file —
#    otherwise the container keeps serving the previously baked image.
(cd infrastructure && docker compose up -d --build)

# 2. Start Microservices
(cd auth-service && docker compose up -d --build) && \
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
(cd auth-service && docker compose down) && \
(cd infrastructure && docker compose down)
```

### Resetting Database & Volumes (Clean State Reset)

To completely wipe all databases, stored volumes, RabbitMQ queues/state, and dynamic dedicated tenant DB containers for a clean restart:

```bash
# 1. Stop microservices and remove local volumes
(cd notification-service && docker compose down -v) && \
(cd order-service && docker compose down -v) && \
(cd user-service && docker compose down -v) && \
(cd tenant-service && docker compose down -v) && \
(cd auth-service && docker compose down -v)

# 2. Stop infrastructure and wipe shared database/message broker volumes
(cd infrastructure && docker compose down -v)

# 3. Remove dynamically provisioned dedicated tenant DB containers & volumes (if any)
docker rm -fv $(docker ps -aq --filter name=postgres-tenant-) 2>/dev/null || true

# 4. Purge Mailpit inbox (mock email messages persist independently of containers)
curl -s -X DELETE "http://localhost:${MAILPIT_DASHBOARD_PORT:-8025}/api/v1/messages" -o /dev/null || true
```

