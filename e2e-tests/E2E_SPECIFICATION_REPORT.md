# End-to-End Test Suite Specification & Verification Report

## 1. Executive Summary

This document serves as the authoritative technical test specification and architectural verification report for the multi-tenant microservices platform. The automated E2E test suite contained within this directory ([`e2e-tests`](./)) validates system-wide guarantees, including asynchronous control plane tenant registration, dynamic isolated database container orchestration, at-least-once message delivery idempotency, fanout cache invalidation, container crash resilience, horizontal scaling concurrency, outbox broker retry survival, singleflight cache stampede prevention, stateful session revocation, zero-trust token forgery rejection, and transactional database DDL rollbacks.

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
  1. Issue HTTP request `POST /api/register` with `plan: "shared"`.
  2. Assert HTTP 202 Accepted response containing `tenant_id` (`tnt_*`) and `user_id` (`usr_*`).
  3. Intercept AMQP event `workspace.initiated` on exchange `company.events`.
  4. Poll `tenant_manager_db.public.tenants` until `status` transitions to `active`.
  5. Query Mailpit REST API (`http://localhost:8025/api/v1/messages`) for welcome notification.
  6. Request password setup token via `POST /internal/auth/setup-token` and complete password setup via `POST /auth/credentials/setup`.
  7. Authenticate via `POST /auth/login` to obtain RS256 JWT access token.
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
  1. Issue HTTP request `POST /api/register` with `plan: "dedicated"`.
  2. Assert HTTP 202 Accepted response.
  3. Verify `infra-provisioner` creates container `postgres-tenant-<id>` with 512MB RAM and 0.5 CPU limits.
  4. Poll `tenant_manager_db.public.tenants` until `status` transitions to `active`.
  5. Query routing metadata in `public.tenant_infrastructures`.
  6. Provision credentials via `POST /internal/auth/setup-token` & `POST /auth/credentials/setup`, then authenticate via `POST /auth/login` to obtain JWT access token.
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
  1. Issue HTTP request `POST /api/register` with malformed email payload (`invalid-email-format`).
* **Expected Guarantee**: Gateway returns HTTP 400 Bad Request before database or message broker operations occur.

---

### 3.8 Test Case TC-E2E-009: Outbox Broadcaster Retry Survival (Docs Case #1)
* **Test File**: [`./tc_e2e_009_outbox_broker_outage_e2e_test.go`](./tc_e2e_009_outbox_broker_outage_e2e_test.go)
* **Objective**: Validate At-Least-Once Delivery and Outbox Worker retry survival when the message broker is temporarily unavailable.
* **Architectural Scope**: Outbox Repository, Outbox Worker, RabbitMQ connection manager.
* **Failure Modes Guarded**: Transactional event loss during broker downtime, crashing background workers.
* **Test Procedure**:
  1. Stop RabbitMQ container (`docker stop rabbitmq`).
  2. Register a tenant via Gateway (`POST /api/register`).
  3. Query `public.outbox` to verify outbox record is safely stored in database (`status = 'PENDING'`).
  4. Restart RabbitMQ container (`docker start rabbitmq`) and wait for TCP connection initialization.
  5. Trigger outbox dead-letter recovery sweeper and poll database until tenant reaches `active` status.
* **Expected Guarantee**: Outbox worker handles broker downtime gracefully without crashing; publishes pending event upon broker recovery; tenant transitions to `active`.

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
  5. Issue `POST /auth/refresh` with the `refresh_token`.
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
  1. Login to obtain `access_token` and `refresh_token`.
  2. Issue `POST /auth/logout` using the `access_token` as authorization header and `refresh_token` in body.
  3. Assert HTTP 200 OK.
  4. Issue `POST /auth/refresh` using the previously valid `refresh_token`.
  5. Assert HTTP 401 Unauthorized.
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
  1. Authenticate as tenant admin and fetch system roles (`GET /api/roles`).
  2. Identify system role (`admin` or `viewer` with `is_system = true`).
  3. Attempt to update system role permissions via `PUT /api/roles/:id/permissions`.
  4. Assert HTTP 400 Bad Request or HTTP 403 Forbidden with `ErrSystemRoleProtected`.
  5. Attempt to delete system role via `DELETE /api/roles/:id`.
  6. Assert HTTP 400 Bad Request or HTTP 403 Forbidden with `ErrSystemRoleProtected`.
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

## 4. Execution Procedures & Verification Commands

To execute the full automated test suite against active local Docker infrastructure:

```bash
cd e2e-tests
export PATH=$PATH:/usr/local/go/bin:/opt/homebrew/bin:$HOME/go/bin
CGO_ENABLED=0 go test -v -timeout 5m ./...
```
