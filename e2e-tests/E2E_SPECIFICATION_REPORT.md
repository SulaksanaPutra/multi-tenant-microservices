# End-to-End Test Suite Specification & Verification Report

## 1. Executive Summary

This document serves as the authoritative technical test specification and architectural verification report for the multi-tenant microservices platform. The automated E2E test suite contained within this directory ([`e2e-tests`](./)) validates system-wide guarantees, including asynchronous control plane tenant registration, dynamic isolated database container orchestration, at-least-once message delivery idempotency, fanout cache invalidation, container crash resilience, horizontal scaling concurrency, outbox broker retry survival, sender-side outbox crash-window deduplication, singleflight cache stampede prevention, stateful session revocation, zero-trust internal endpoint boundaries, RBAC deny-path enforcement, token rotation/replay rejection, zero-trust token forgery rejection, and transactional database DDL rollbacks.

---

## 2. System Architecture & Component Topology

### 2.1 Service Topology & Port Specifications
* **API Gateway**: Traefik (HTTP reverse proxy entrypoint on port `8000`).
* **Auth Service**: Standalone authentication service (`auth-service` on port `8085`), managing user credentials (`public.user_credentials`), opaque refresh tokens (`public.refresh_tokens`), password setup tokens (`public.password_setup_tokens`), and RS256 JWT access token issuance/JWKS distribution (`/.well-known/jwks.json`).
* **Message Broker**: RabbitMQ (AMQP 0-9-1 on port `5672`, Management REST API on port `15672`).
* **Control Plane Database**: PostgreSQL (`tenant_manager_db` on port `5432`).
* **Data Plane Isolation**:
  * **Shared Plan**: Schema-per-tenant (`tenant_<slug>_order_db`) on PostgreSQL `shared_db`.
  * **Dedicated Plan**: Independent PostgreSQL Docker container (`postgres-tenant-<id>`) with compute resource limits (512MB RAM, 0.5 CPU).
* **Notification System**: Mailpit (SMTP server on port `1025`, REST API on port `8025`).

---

## 3. Test Cases Specification & Verification Matrix

### 3.1 Test Case TC-E2E-001: Shared Plan Multi-Tenant Registration & Order Lifecycle
* **Test File**: [`./tc_e2e_001_register_shared_plan_e2e_test.go`](./tc_e2e_001_register_shared_plan_e2e_test.go)
* **Objective**: Validate asynchronous control plane registration workflow, schema-per-tenant isolation, Mailpit notification dispatch, setup-token credential provisioning, JWT authentication, and multi-tenant order execution.
* **Architectural Scope**: `user-service`, `tenant-service`, `auth-service`, `infra-provisioner`, `order-service`, `notification-service`.
* **Failure Modes Guarded**: Cross-tenant data leakage, unauthenticated order writes, asynchronous provisioning race conditions.
* **Test Procedure**:
  1. Issue HTTP request `POST /api/tenants/register` with `plan: "shared"`.
  2. Poll database until `tenants.status` transitions from `provisioning` to `active`.
  3. Verify schema isolation: confirm dedicated schema `shared_db` contains database tables `orders` and `outbox_events`.
  4. Verify AMQP fanout: check exchange `company.events` received routing keys `workspace.initiated` and `infrastructure.provisioned`.
  5. Query Mailpit REST API (`http://localhost:8025/api/v1/messages`) for welcome notification.
  6. Extract single-use setup token from welcome email and establish password.
  7. Authenticate via `POST /api/auth/login` to obtain RS256 JWT access token.
  8. Issue HTTP request `GET /api/notifications` with `Authorization: Bearer <accessToken>` header and assert HTTP 200 OK.
  9. Issue HTTP request `POST /api/orders` with `Authorization: Bearer <accessToken>` header and assert HTTP 201 Created.
  10. Issue HTTP request `GET /api/orders` with `Authorization: Bearer <accessToken>` header and assert order retrieval from `shared_db`.
* **Expected Guarantee**: System executes full asynchronous lifecycle cleanly; user credentials provisioned; order data isolated in schema `tenant_<slug>_order_db`.

---

### 3.2 Test Case TC-E2E-002: Dedicated Plan Dynamic Container Provisioning
* **Test File**: [`./tc_e2e_002_register_dedicated_plan_e2e_test.go`](./tc_e2e_002_register_dedicated_plan_e2e_test.go)
* **Objective**: Validate dynamic Docker container orchestration, health check polling, Zero-Trust role/schema bootstrapping, JWT bearer token authentication, and private container order execution.
* **Architectural Scope**: `auth-service`, `infra-provisioner`, `order-service`, Docker Daemon (`/var/run/docker.sock`).
* **Failure Modes Guarded**: Tenant compute interference, docker socket privilege escalation leaks, database user privilege over-granting.
* **Test Procedure**:
  1. Issue HTTP request `POST /api/tenants/register` with `plan: "dedicated"`.
  2. Poll database until `tenants.status` transitions from `provisioning` to `active`.
  3. Verify schema isolation: confirm dedicated schema `tenant_<id>` is created with isolated tables.
  4. Verify AMQP fanout: check exchange `company.events` received routing keys `workspace.initiated` and `infrastructure.provisioned`.
  5. Query Mailpit REST API (`http://localhost:8025/api/v1/messages`) for welcome notification.
  6. Provision credentials via `POST /internal/auth/setup-token` & `POST /api/auth/credentials/setup`, then authenticate via `POST /api/auth/login` to obtain JWT access token.
  7. Issue HTTP request `POST /api/orders` with `Authorization: Bearer <accessToken>` header targeting the dedicated container database.
  8. Issue HTTP request `GET /api/orders` with `Authorization: Bearer <accessToken>` header and verify order retrieval.
* **Expected Guarantee**: Container provisioned, healthy, bootstrapped with domain user `order_user`, public schema privileges granted, and order data isolated on dedicated container compute.

---

### 3.3 Test Case TC-E2E-003: Inbox Deduplication & Idempotent Processing (Docs Case #2)
* **Test File**: [`./tc_e2e_003_inbox_deduplication_e2e_test.go`](./tc_e2e_003_inbox_deduplication_e2e_test.go)
* **Objective**: Validate at-least-once message delivery idempotency and prevention of PostgreSQL transaction abortion under duplicate AMQP message delivery.
* **Architectural Scope**: `InboxRepository`, AMQP Consumers (`notification-service`, `order-service`).
* **Failure Modes Guarded**: Duplicate domain processing side-effects, double email notifications, duplicate DB mutations under RabbitMQ redeliveries.
* **Test Procedure**:
  1. Register a tenant and wait for activation.
  2. Publish a synthetic `tenant.order_db.ready` AMQP message with event ID `evt_duplicate_test_*`.
  3. Immediately publish a duplicate AMQP message with the exact same event ID.
  4. Query `public.inbox` for row count matching `evt_duplicate_test_*`.
* **Expected Guarantee**: Duplicate event trapped by `ON CONFLICT (event_id) DO NOTHING`; inbox record count equals 1; zero duplicate processing side-effects.

---

### 3.4 Test Case TC-E2E-004: Fanout Exchange Broadcast & Cache Invalidation (Docs Case #13 & #14)
* **Test File**: [`./tc_e2e_004_infrastructure_fanout_e2e_test.go`](./tc_e2e_004_infrastructure_fanout_e2e_test.go)
* **Objective**: Validate multi-instance in-memory cache eviction (`PoolRegistry` and DSN routing metadata) upon infrastructure migration.
* **Architectural Scope**: RabbitMQ Fanout Exchange `company.events`, `order-service` replicas.
* **Failure Modes Guarded**: Stale DSN connection routing following tenant database migration or scaling.
* **Test Procedure**:
  1. Register tenant, wait for activation, obtain JWT access token, and issue `POST /api/orders` to populate `order-service` connection cache.
  2. Publish `tenant.infrastructure_changed` event over AMQP fanout exchange.
  3. Issue follow-up `GET /api/orders` request with `Authorization: Bearer <accessToken>` header.
* **Expected Guarantee**: Event received on exclusive auto-delete queues across all replicas; local connection cache purged; subsequent requests re-resolve fresh DSN metadata cleanly.

---

### 3.5 Test Case TC-E2E-005: Container Outage Survival & Queue Catch-Up
* **Test File**: [`./tc_e2e_005_service_outage_recovery_e2e_test.go`](./tc_e2e_005_service_outage_recovery_e2e_test.go)
* **Objective**: Validate system fault tolerance during consumer container crashes, AMQP queue durability, and eventual consistency upon recovery.
* **Architectural Scope**: `notification-service`, RabbitMQ durable queues.
* **Failure Modes Guarded**: Message drop during consumer crashes, lost notification emails.
* **Test Procedure**:
  1. Stop container `notification-service` (`docker stop notification-service`).
  2. Register a new tenant via Gateway.
  3. Verify tenant reaches `active` status while `notification-service` is offline.
  4. Verify welcome email is not present in Mailpit.
  5. Restart container `notification-service` (`docker start notification-service`).
  6. Poll Mailpit REST API for welcome notification delivery.
* **Expected Guarantee**: Events accumulate safely in persistent RabbitMQ queues; upon container startup, consumer drains queue, writes to `inbox`, and delivers email without data loss.

---

### 3.6 Test Case TC-E2E-006: Multi-Replica Scaling & Concurrency Control
* **Test File**: [`./tc_e2e_006_multi_replica_scaling_e2e_test.go`](./tc_e2e_006_multi_replica_scaling_e2e_test.go)
* **Objective**: Validate horizontal scaling of microservice replicas, Traefik round-robin load balancing, and outbox worker concurrency safety via `FOR UPDATE SKIP LOCKED`.
* **Architectural Scope**: Scaled `order-service` replicas, Traefik Gateway.
* **Failure Modes Guarded**: Concurrent outbox polling race conditions, duplicate event dispatch across worker instances.
* **Test Procedure**:
  1. Scale `order-service` to 2 container replicas.
  2. Register tenant and wait for activation.
  3. Issue 5 concurrent order creation requests (`POST /api/orders`).
* **Expected Guarantee**: Traffic load-balanced across replicas with 100% HTTP 201 Created success rate; zero outbox lock contention.

---

### 3.7 Test Case TC-E2E-007: Gateway Validation & Input Sanitization
* **Test File**: [`./tc_e2e_007_register_e2e_test.go`](./tc_e2e_007_register_e2e_test.go)
* **Objective**: Validate edge-case input rejection and gateway error handling.
* **Architectural Scope**: API Gateway validation layer, Gin binding controllers.
* **Failure Modes Guarded**: Malformed payload propagation to internal queue layers, SQL/no-SQL injection attempts.
* **Test Procedure**:
  1. Issue HTTP request `POST /api/tenants/register` with malformed email payload (`invalid-email-format`).
  2. Verify system returns HTTP 400 Bad Request and aborts transaction before hitting DB or MQ.
  3. Attempt duplicate tenant registration with identical `tenant_name`.
  4. Assert unique constraint error or handling strategy.

### 3.8 Test Case TC-E2E-008/009: Outbox Event Broker Publishing, At-Least-Once Delivery & Broker Outage Retry Survival (Docs Case #1)
* **Test File**: [`./tc_e2e_009_outbox_broker_outage_e2e_test.go`](./tc_e2e_009_outbox_broker_outage_e2e_test.go)
* **Objective**: Verify the outbox pattern guarantees at-least-once delivery and that the background outbox worker survives a temporary message-broker outage without crashing or losing transactional events.
* **Architectural Scope**: Outbox Repository (`public.outbox`), Outbox Worker, RabbitMQ AMQP connection manager.
* **Failure Modes Guarded**: Transactional event loss during broker downtime, crashing background workers, event drop on broker recovery.
* **Test Procedure**:
  1. Stop RabbitMQ container (`docker stop rabbitmq`) to simulate broker downtime.
  2. Register a tenant via Gateway (`POST /api/tenants/register`) and assert HTTP 202 Accepted (outbox decouples HTTP write from AMQP publication).
  3. Query `public.outbox` and verify the event is safely persisted in `PENDING`/`PROCESSING` status.
  4. Restart RabbitMQ and wait for AMQP TCP readiness.
  5. Trigger the outbox dead-letter sweeper and poll `public.tenants` until status transitions to `active`.
* **Expected Guarantee**: Outbox worker handles broker downtime gracefully without crashing; publishes the pending event upon broker recovery; tenant transitions to `active` with zero data loss.

---

### 3.9 Test Case TC-E2E-010: Cache Stampede Prevention via Singleflight (Docs Case #11)
* **Test File**: [`./tc_e2e_010_cache_stampede_singleflight_e2e_test.go`](./tc_e2e_010_cache_stampede_singleflight_e2e_test.go)
* **Objective**: Validate that `singleflight` request coalescing prevents database connection cache stampedes under high concurrency.
* **Architectural Scope**: `TenantDBResolver`, `singleflight.Group`, `PoolRegistry`.
* **Failure Modes Guarded**: Connection pool depletion and high query latency on cold cache cache stampedes.
* **Test Procedure**:
  1. Register a tenant and wait for activation (when connection cache is empty).
  2. Issue 50 concurrent `GET /api/orders` HTTP requests simultaneously.
* **Expected Guarantee**: 100% of concurrent requests succeed with HTTP 200 OK; singleflight barrier coalesces calls into a single routing RPC and connection pool setup.

---

### 3.10 Test Case TC-E2E-011: Transactional DDL Migration Rollback Safety (Docs Case #4)
* **Test File**: [`./tc_e2e_011_transactional_ddl_rollback_e2e_test.go`](./tc_e2e_011_transactional_ddl_rollback_e2e_test.go)
* **Objective**: Verify database schema safety and atomic rollback if SQL DDL migrations fail midway.
* **Architectural Scope**: PostgreSQL DDL transaction engine.
* **Failure Modes Guarded**: Partially applied schemas, corrupted database migrations, orphaned database objects.
* **Test Procedure**:
  1. Begin DDL transaction on PostgreSQL connection.
  2. Execute valid table creation statement (`CREATE TABLE valid_table_before_failure`).
  3. Execute invalid DDL statement (`CREATE TABLE bad_table (id NON_EXISTENT_TYPE)`), triggering a SQL syntax error midway.
  4. Call `tx.Rollback()`.
  5. Query `information_schema.tables` for the test schema.
* **Expected Guarantee**: PostgreSQL transaction rolls back atomically; `valid_table_before_failure` creation is completely undone; zero partial tables remain.

---

### 3.11 Test Case TC-E2E-012: Stateful Token Refresh Lifecycle
* **Test File**: [`./tc_e2e_012_stateful_token_refresh_e2e_test.go`](./tc_e2e_012_stateful_token_refresh_e2e_test.go)
* **Objective**: Validate the hybrid authentication model by simulating an expired short-lived JWT, confirming downstream rejection, and successfully rotating credentials via the stateful refresh token.
* **Architectural Scope**: `auth-service` (Refresh endpoint), `order-service` (JWT Middleware).
* **Failure Modes Guarded**: Expired token acceptance, static un-rotatable access credentials.
* **Test Procedure**:
  1. Register tenant, provision credentials, and login to obtain `access_token` and `refresh_token`.
  2. Generate a synthetic JWT signed with the valid RSA private key but an `exp` claim set to 5 minutes ago.
  3. Issue `GET /api/orders` with the expired `access_token`.
  4. Assert HTTP 401 Unauthorized.
  5. Issue `POST /api/auth/refresh` with the `refresh_token`.
  6. Assert HTTP 200 OK and extract the new `access_token`.
  7. Issue `GET /api/orders` with the new token and assert HTTP 200 OK.
* **Expected Guarantee**: Downstream services correctly reject expired JWTs locally; `auth-service` successfully validates the opaque token against `public.refresh_tokens` and issues a new pair.

---

### 3.12 Test Case TC-E2E-013: Immediate Session Revocation (Logout)
* **Test File**: [`./tc_e2e_013_session_revocation_e2e_test.go`](./tc_e2e_013_session_revocation_e2e_test.go)
* **Objective**: Validate the stateful revocation capability, ensuring that once a refresh token is explicitly revoked, the user cannot acquire new access tokens.
* **Architectural Scope**: `auth-service` (`public.refresh_tokens`).
* **Failure Modes Guarded**: Post-logout unauthorized token acquisition, stolen refresh token persistence.
* **Test Procedure**:
  1. Register tenant & authenticate user.
  2. Issue `POST /api/auth/logout` using the `access_token` as authorization header and `refresh_token` in body.
  3. Verify Auth Service sets `revoked_at` timestamp in `public.refresh_tokens`.
  4. Issue `POST /api/auth/refresh` using the previously valid `refresh_token`.
  5. Verify Auth Service rejects refresh attempt with HTTP 401 Unauthorized.
* **Expected Guarantee**: The `refresh_tokens` row is marked as revoked in the database; subsequent refresh attempts are permanently denied.

---

### 3.13 Test Case TC-E2E-014: Password Setup Token Single-Use Idempotency
* **Test File**: [`./tc_e2e_014_password_setup_idempotency_e2e_test.go`](./tc_e2e_014_password_setup_idempotency_e2e_test.go)
* **Objective**: Prevent account hijacking race conditions by validating that a setup token can only be consumed exactly once.
* **Architectural Scope**: `auth-service` (`public.password_setup_tokens`).
* **Failure Modes Guarded**: Token replay attacks, account hijacking during onboarding.
* **Test Procedure**:
  1. Trigger a passwordless registration and fetch setup token via `/internal/auth/setup-token`.
  2. Issue `POST /auth/credentials/setup` with the valid token. Assert HTTP 200 OK.
  3. Immediately issue the exact same `POST /auth/credentials/setup` request again.
  4. Assert HTTP 400 Bad Request (or 409 Conflict) with an `ErrTokenAlreadyUsed` payload.
* **Expected Guarantee**: Database enforces `used_at` mutation atomically; token cannot be reused by malicious actors.

---

### 3.14 Test Case TC-E2E-015: Decentralized Authorization Resilience (Auth Service Outage)
* **Test File**: [`./tc_e2e_015_decentralized_auth_resilience_e2e_test.go`](./tc_e2e_015_decentralized_auth_resilience_e2e_test.go)
* **Objective**: Prove that local RS256 JWT verification prevents the `auth-service` from becoming a synchronous bottleneck or single point of failure for existing active sessions.
* **Architectural Scope**: `order-service` JWT Middleware, Docker runtime.
* **Failure Modes Guarded**: Centralized auth server single-point-of-failure bottlenecks.
* **Test Procedure**:
  1. Login via Gateway to obtain a valid 15-minute `access_token`.
  2. Stop the Auth container (`docker stop auth-service`).
  3. Issue `POST /api/orders` with the valid `access_token`.
  4. Assert HTTP 201 Created.
  5. Restart the Auth container (`docker start auth-service`).
* **Expected Guarantee**: `order-service` successfully verifies the JWT mathematically using its cached public key and processes the order entirely independently of `auth-service`'s uptime.

---

### 3.15 Test Case TC-E2E-016: Malicious Token Forgery Prevention (RS256 vs HS256)
* **Test File**: [`./tc_e2e_016_token_forgery_prevention_e2e_test.go`](./tc_e2e_016_token_forgery_prevention_e2e_test.go)
* **Objective**: Validate the zero-trust cryptographic boundary by ensuring downstream services strictly enforce RS256 verification and reject forged symmetric signatures.
* **Architectural Scope**: `order-service` JWT Middleware.
* **Failure Modes Guarded**: JWT algorithm confusion vulnerability (signing HS256 tokens using the public key string).
* **Test Procedure**:
  1. Obtain the public JWKS key from the test environment.
  2. Create a custom JWT payload with valid claims.
  3. Sign the custom JWT using `HS256` algorithm, using the public key string as the symmetric secret (algorithm confusion attack).
  4. Issue `GET /api/orders` with the forged token.
  5. Assert HTTP 401 Unauthorized.
* **Expected Guarantee**: The JWT middleware strictly verifies the `alg` header is `RS256` and successfully rejects the forged token before it reaches application logic.

---

### 3.16 Test Case TC-E2E-017: Multi-Tenant Custom Role CRUD & Instant Permission Invalidation (Docs Case #18 & #19)
* **Test File**: [`./tc_e2e_017_custom_role_crud_e2e_test.go`](./tc_e2e_017_custom_role_crud_e2e_test.go)
* **Objective**: Validate tenant-scoped custom role creation, atomic user permission version batch-incrementing, and instant downstream token revocation (`VersionCache` invalidation).
* **Architectural Scope**: `auth-service`, `order-service`, `user-service`, `notification-service`.
* **Failure Modes Guarded**: Post-revocation unauthorized access using non-expired JWT access tokens, role permission drift.
* **Test Procedure**:
  1. Register tenant, setup credentials, and obtain JWT access token with initial permissions (`perm_version = 1`).
  2. Create custom role via `POST /api/roles` and update role permissions via `PUT /api/roles/:id/permissions`.
  3. Verify `auth-service` batch-increments `user_permission_versions.version` to `2`.
  4. Issue request `POST /api/orders` with initial JWT access token (`perm_version = 1`).
  5. Assert HTTP 401 Unauthorized response rejection (`token superseded: permissions updated`).
* **Expected Guarantee**: Modifying tenant role permissions immediately invalidates downstream access tokens on version check mismatch before token expiration.

---

### 3.17 Test Case TC-E2E-018: System Default Role Protection & Guardrails
* **Test File**: [`./tc_e2e_018_system_default_role_protection_e2e_test.go`](./tc_e2e_018_system_default_role_protection_e2e_test.go)
* **Objective**: Validate platform guardrails protecting pre-seeded system default roles (`admin`, `viewer`) from modification or deletion by tenant admins.
* **Architectural Scope**: `auth-service` Role Management API.
* **Failure Modes Guarded**: Accidental or malicious mutation/deletion of platform system roles, system stability degradation.
* **Test Procedure**:
  1. Authenticate as tenant admin and fetch system roles (`GET /api/auth/roles`).
  2. Identify immutable system roles (`admin`, `viewer`).
  3. Attempt to update system role permissions via `PUT /api/auth/roles/:id/permissions`.
  4. Assert HTTP 400 Bad Request or HTTP 403 Forbidden rejection.
  5. Attempt to delete system role via `DELETE /api/auth/roles/:id`.
  6. Assert HTTP 400 Bad Request or HTTP 403 Forbidden rejection.th `ErrSystemRoleProtected`.
* **Expected Guarantee**: Platform system default roles remain immutably protected against modification or deletion.

---

### 3.18 Test Case TC-E2E-019: Cross-Tenant Permission Isolation Boundary
* **Test File**: [`./tc_e2e_019_cross_tenant_permission_isolation_e2e_test.go`](./tc_e2e_019_cross_tenant_permission_isolation_e2e_test.go)
* **Objective**: Validate strict multi-tenant boundary isolation, ensuring valid JWT access tokens issued for Tenant A cannot access or mutate resources of Tenant B.
* **Architectural Scope**: Gateway, `order-service`, `user-service`, `notification-service`.
* **Failure Modes Guarded**: Multi-tenant data leakage, lateral authorization bypass between tenants.
* **Test Procedure**:
  1. Register two independent tenants: Tenant A (`tnt_a`) and Tenant B (`tnt_b`).
  2. Complete credential setup and log in to obtain JWT access tokens for both tenants (`jwt_a` and `jwt_b`).
  3. Create an order under Tenant A (`POST /api/orders` with `jwt_a`).
  4. Attempt to query or mutate Tenant A's order using `jwt_b` or forging `tenant_id` query/header parameters.
  5. Query `GET /api/orders` using `jwt_b`.
* **Expected Guarantee**: Requests executed with `jwt_b` only observe resources in Tenant B's isolated database schema/container; zero visibility or access to Tenant A resources.

---

### 3.19 Test Case TC-E2E-020: User Profile Management & Listing
* **Test File**: [`./tc_e2e_020_user_profile_management_e2e_test.go`](./tc_e2e_020_user_profile_management_e2e_test.go)
* **Objective**: Validate user listing within tenant boundaries (`GET /api/users`) and profile updating (`PUT /api/users/me`).
* **Architectural Scope**: `user-service`, Traefik Gateway.
* **Expected Guarantee**: Users can list tenant members and modify their own profile name securely under verified JWT token claims.

---

### 3.20 Test Case TC-E2E-021: Tenant Control Plane Management & Isolation Plan Upgrade
* **Test File**: [`./tc_e2e_021_tenant_management_and_plan_upgrade_e2e_test.go`](./tc_e2e_021_tenant_management_and_plan_upgrade_e2e_test.go)
* **Objective**: Validate tenant profile listing (`GET /api/tenants`), metadata modification (`PUT /api/tenants?tenant_id=...`), plan switching (`PUT /api/tenants/plan?tenant_id=...`), and cross-tenant parameter tampering prevention (`403 Forbidden`).
* **Architectural Scope**: `tenant-service`, Control Plane Database (`tenant_manager_db`).
* **Expected Guarantee**: Tenant admins can retrieve and update their tenant profile/plan while parameter mismatch attempts are blocked with 403 Forbidden.

---

### 3.21 Test Case TC-E2E-022: User Role Assignment & System Permissions Catalog
* **Test File**: [`./tc_e2e_022_user_role_assignment_and_permissions_e2e_test.go`](./tc_e2e_022_user_role_assignment_and_permissions_e2e_test.go)
* **Objective**: Validate fetching system permissions catalog (`GET /api/auth/permissions`), creating custom tenant roles (`POST /api/auth/roles`), listing roles (`GET /api/auth/roles`), and assigning roles to users (`PUT /api/auth/users/:userID/role`).
* **Architectural Scope**: `auth-service` (Access plane).
* **Expected Guarantee**: System permissions catalog is inspectable and can be composed into custom tenant roles and assigned to users.

---

### 3.22 Test Case TC-E2E-023: Notification Center Log Retrieval
* **Test File**: [`./tc_e2e_023_notification_center_e2e_test.go`](./tc_e2e_023_notification_center_e2e_test.go)
* **Objective**: Validate token-gated retrieval of tenant notification logs (`GET /api/notifications`).
* **Architectural Scope**: `notification-service`.
* **Expected Guarantee**: Authenticated users with `notifications:read` permission can retrieve notification dispatch logs.

---

### 3.23 Test Case TC-E2E-024: Same-Email Multi-Tenant Registration & Unified Identity (Docs Case #21)
* **Test File**: [`./tc_e2e_024_same_email_multi_tenant_registration_e2e_test.go`](./tc_e2e_024_same_email_multi_tenant_registration_e2e_test.go)
* **Objective**: Validate that the same email address can independently register multiple distinct tenant workspaces (shared & dedicated plans) without unique-constraint failures or AMQP barrier sync deadlocks, and that credentials, JWT tokens, and isolated profiles are bound to their respective `tenant_id`.
* **Architectural Scope**: Control Plane Registration, composite uniqueness `(tenant_id, email)`, unified identity, workspace selection.
* **Failure Modes Guarded**: Registration deadlocks on shared email, cross-tenant JWT claim confusion, credential cross-binding.
* **Test Procedure**:
  1. Register Tenant 1 (shared) and Tenant 2 (dedicated) under the exact same `owner_email`.
  2. Verify both tenants reach `active` and hold distinct `tenant_id` values.
  3. Verify unified user profile in `user_db` and provision credentials for both memberships.
  4. Authenticate with workspace selection and assert each JWT embeds the correct `tenant_id` claim.
* **Expected Guarantee**: Unified identity supports multi-tenant memberships; workspace selection issues isolated, correctly-scoped JWTs.

---

### 3.24 Test Case TC-E2E-025: Sender-Side Outbox Crash-Window Duplicate Republish (Docs Case #3, Full Loop)
* **Test File**: [`./tc_e2e_025_outbox_crash_window_duplicate_e2e_test.go`](./tc_e2e_025_outbox_crash_window_duplicate_e2e_test.go)
* **Objective**: Validate the complete outbox+inbox closed loop for the "phantom batch" crash window: message published → sender crashes before `MarkPublished` → the outbox row is reset to `PENDING` (reproducing the post-crash state that `RecoverStuckClaims` produces for a stuck `PROCESSING` claim) → worker republishes the identical event. The downstream inbox barrier must trap the duplicate so exactly ONE inbox record and exactly ONE welcome email remain.
* **Architectural Scope**: `public.outbox`, Outbox Worker (`FOR UPDATE SKIP LOCKED`), `public.inbox`, `notification-service`, Mailpit.
* **Failure Modes Guarded**: Duplicate domain side-effects after publish-then-crash-then-republish, duplicate welcome emails under sender-side at-least-once delivery.
* **Test Procedure**:
  1. Register a shared tenant and await activation.
  2. Resolve the tenant's `workspace.ready` outbox row (`PUBLISHED`) — its row ID doubles as the inbox `event_id`.
  3. Assert baseline: inbox row count = 1 and Mailpit welcome email count = 1.
  4. Force the outbox row back to `PENDING` (sweeper-style reset; production sweeper only resets `PROCESSING` rows older than 30s) and wait for the worker to republish it.
  5. Poll until the duplicate has traversed the inbox barrier; assert final inbox count still = 1 and Mailpit email count still = 1.
* **Expected Guarantee**: `ON CONFLICT (event_id) DO NOTHING` traps the republished duplicate; zero duplicate side-effects end-to-end.

---

### 3.25 Test Case TC-E2E-026: Internal Zero-Trust Endpoint Boundary (Docs Cases #8 & #9)
* **Test File**: [`./tc_e2e_026_internal_endpoint_zero_trust_e2e_test.go`](./tc_e2e_026_internal_endpoint_zero_trust_e2e_test.go)
* **Objective**: Validate the zero-trust control-plane boundary: internal endpoints (`/internal/auth/*`, `/internal/tenants/*`) must reject requests lacking a valid `X-Internal-Service-Token` and must never leak infrastructure metadata via path traversal, unknown service names, or nonexistent tenants.
* **Architectural Scope**: `auth-service`, `tenant-service`, `InternalAuthMiddleware`.
* **Failure Modes Guarded**: Lateral movement into control-plane internals, SSRF/host-takeover via `service_name`, cross-service credential theft, infrastructure metadata leakage.
* **Test Procedure**:
  1. Register a tenant and resolve a real `tenant_id`/`user_id`.
  2. Probe `POST /internal/auth/setup-token` and `GET /internal/auth/users/:userID/perm-version` with missing, forged, and valid internal tokens (403 / 403 / 200).
  3. Probe `GET /internal/tenants/:id/infrastructure/order-service` with missing, forged, and valid tokens (403 / 403 / 200).
  4. Probe with a path-traversal `service_name`, an unknown service, and a nonexistent tenant — assert HTTP 404 with zero metadata leakage.
* **Expected Guarantee**: Internal endpoints are strictly token-gated; unknown/traversal inputs yield 404, never data.

---

### 3.26 Test Case TC-E2E-027: RBAC Deny-Path Enforcement (403 Forbidden Matrix)
* **Test File**: [`./tc_e2e_027_rbac_deny_path_e2e_test.go`](./tc_e2e_027_rbac_deny_path_e2e_test.go)
* **Objective**: Validate the NEGATIVE authorization plane. A token minted for a read-only role must be denied at every write/manage boundary (`403 Forbidden`) while retaining read access (`200 OK`), proving least-privilege enforcement by default-deny.
* **Architectural Scope**: `auth-service` Role API + permission resolution, `order-service`/`tenant-service`/`user-service` `RequirePermission`.
* **Failure Modes Guarded**: Authorization bypass via over-privileged roles, missing permission checks on write endpoints, RBAC drift after role reassignment.
* **Test Procedure**:
  1. Register tenant, provision credentials, login as admin.
  2. Create a custom role containing only `orders:read` and reassign the owner user to it (permission version batch-increments).
  3. Login again to obtain a token reflecting the read-only permission set.
  4. Assert `403` on `POST /api/orders`, `PUT /api/tenants/me/plan`, `POST /api/auth/roles`, `GET /api/auth/permissions`.
  5. Assert `200` on `GET /api/orders`.
* **Expected Guarantee**: Read-only tokens are denied at every gated write/manage boundary while read access succeeds.

---

### 3.27 Test Case TC-E2E-028: Stateful Token Rotation, Replay Rejection & Workspace Exchange Security
* **Test File**: [`./tc_e2e_028_refresh_token_rotation_replay_e2e_test.go`](./tc_e2e_028_refresh_token_rotation_replay_e2e_test.go)
* **Objective**: Validate the token-lifecycle abuse plane. (A) Refresh-token rotation: after a successful refresh the old token is deleted and replays yield `401`. (B) Workspace-selection exchange tokens are single-use: replaying a consumed exchange token, selecting a non-member tenant, or using a forged token all yield `400`.
* **Architectural Scope**: `auth-service` (`RefreshHandler`, `SelectWorkspaceHandler`, `public.refresh_tokens`, `public.password_setup_tokens`), unified-identity workspace selection.
* **Failure Modes Guarded**: Session hijacking via stolen refresh-token replay, indefinite token reuse, cross-tenant escalation through workspace exchange tokens.
* **Test Procedure**:
  1. Login, refresh → new pair; replay the rotated token → `401`; repeat the chain once more.
  2. Register two tenants under the same email; login → `SELECT_WORKSPACE` exchange token.
  3. Select Tenant A → `200`; replay the same exchange token for Tenant B → `400`.
  4. Fresh login; select a non-member tenant → `400`; submit a forged exchange token → `400`.
* **Expected Guarantee**: Rotated refresh tokens are permanently rejected and workspace exchange tokens are strictly single-use and membership-scoped.

---

### 3.28 Test Case TC-E2E-029: Order-Service (Data-Plane Consumer) Outage & Queue Catch-Up Recovery
* **Test File**: [`./tc_e2e_029_order_service_outage_recovery_e2e_test.go`](./tc_e2e_029_order_service_outage_recovery_e2e_test.go)
* **Objective**: Validate fault tolerance of the DATA-PLANE consumer path. While `order-service` is offline, `infrastructure.provisioned` events must accumulate in the durable `order_service_infra_provisioned` queue, the tenant must remain pending (not active), and upon container restart the tenant must activate with orders fully functional.
* **Architectural Scope**: `order-service` (`InfrastructureProvisionedConsumer`, `PoolRegistry` bootstrap), `tenant-service`, `infra-provisioner`, Traefik Gateway.
* **Failure Modes Guarded**: Lost routing/activation events during order-service crashes, broken `PoolRegistry` bootstrap after restart, zombie `pending` tenants after recovery.
* **Test Procedure**:
  1. Stop `order-service` (`docker stop order-service`).
  2. Register a tenant via Gateway and assert the tenant does NOT reach `active` (activation is blocked on `tenant.order_db.ready`).
  3. Restart `order-service` (`docker start order-service`).
  4. Poll `tenant_manager_db` until the tenant transitions to `active`.
  5. Provision credentials, login, and assert `POST /api/orders` returns HTTP 201 Created.
* **Expected Guarantee**: Buffered events drain safely on restart; tenant activates; data-plane order execution functions post-recovery.
* **Operational Preconditions**:
  - Requires host Docker CLI access with permission to stop/start the `order-service` container.
  - The durable queue `order_service_infra_provisioned` must already exist in RabbitMQ (declared by a prior `order-service` start). On a fresh RabbitMQ where `order-service` never declared it, `infrastructure.provisioned` messages are silently dropped and the tenant never activates.
  - Must run serially (`-p 1`); stopping `order-service` while other tests execute breaks them.

---

## 4. Execution Procedures & Verification Commands

To execute the full automated test suite against active local Docker infrastructure:

```bash
cd e2e-tests
export PATH=$PATH:/usr/local/go/bin:/opt/homebrew/bin:$HOME/go/bin
CGO_ENABLED=0 go test -v -p 1 -timeout 10m ./...
```

> **Serial execution is REQUIRED.** Several destructive tests stop/start shared containers (`rabbitmq`, `notification-service`, `auth-service`, `order-service`) and mutate shared broker/database state. Running with `-p 1` (or `-parallel 1`) prevents cross-test interference. Do not call `t.Parallel()` inside destructive tests.

---

## 5. Known Architectural Limitations & Open Design Decisions

The E2E suite surfaced three architectural behaviors that are either intentional trade-offs or incomplete features. They are documented here so the report reflects reality rather than aspiration:

### 5.1 Tenant "Plan Upgrade" Is Metadata-Only (No Infrastructure Migration)
`PUT /api/tenants/me/plan` only updates the `plan` column in `public.tenants` ([tenant_repository.go](../tenant-service/internal/repository/tenant_repository.go)). It does **not** publish `tenant.infrastructure_changed`, does not trigger `infra-provisioner` container orchestration, and does not migrate data from the shared schema to a dedicated container. **TC-E2E-021 therefore validates a DB column flip, not a true plan migration.** A real migration contract test (proposed TC-E2E-030) cannot pass until the feature is implemented. Decision required: implement real duality migration, or scope the documented behavior accordingly.

### 5.2 "Instant JWT Revocation" Has a 60-Second Cache Window
`VersionCache` in downstream services ([jwt_middleware.go](../order-service/internal/middleware/jwt_middleware.go)) caches the user permission version for `ttl: 60s`. If a request succeeds with `perm_version = 1` *before* a role-permission bump, the cached entry keeps `1 <= 1` true for up to 60 seconds — a revoked token remains accepted during that window. **TC-E2E-017 validates the cold-cache path only.** Any claim of "instant revocation" must either accept this window or shorten/remove the TTL.

### 5.3 Permission-Version Verification Fails Open During Auth-Service Outage
`VerifyVersion` returns `true` on any upstream error ([jwt_middleware.go](../order-service/internal/middleware/jwt_middleware.go)). When `auth-service` is unreachable, JWT *signature* verification continues locally (TC-E2E-015), but permission-version *revocation* silently stops enforcing. This is a deliberate availability-vs-security trade-off; the fail-open behavior is not yet covered by an explicit test.

### 5.4 Test-Suite Document Consistency
* The broker-outage test shipped as `tc_e2e_009_*` and is mapped to this report's TC-E2E-008/009 section.
* TC-E2E-024 (`tc_e2e_024_*`) predates this report revision and is now documented in §3.23.
* TC-E2E-019's mention of "forging `tenant_id` query/header parameters" is illustrative; the API derives tenant context strictly from JWT claims.
