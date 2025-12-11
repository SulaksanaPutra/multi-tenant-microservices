# End-to-End Test Suite Specification & Verification Report

## 1. Executive Summary

This document serves as the formal technical test specification and architectural verification report for the multi-tenant microservices platform. The automated test suite contained within this directory validates core system guarantees, including event-driven control plane registration, dynamic database container orchestration, at-least-once message delivery idempotency, fanout cache invalidation, container crash resilience, and horizontal scaling concurrency.

---

## 2. System Architecture & Test Pre-conditions

### 2.1 Component Topology
* **API Gateway**: Traefik (HTTP entrypoint on port `8000`).
* **Message Broker**: RabbitMQ (AMQP 0-9-1 on port `5672`, Management UI on port `15672`).
* **Control Plane Database**: PostgreSQL (`tenant_manager_db` on port `5432`).
* **Data Plane Isolation**:
  * **Shared Plan**: Schema-per-tenant (`tenant_<slug>_order_db`) on PostgreSQL `shared_db`.
  * **Dedicated Plan**: Independent PostgreSQL Docker container (`postgres-tenant-<id>`) with compute limits (512MB RAM, 0.5 CPU).
* **Notification System**: Mailpit (SMTP server on port `1025`, REST API on port `8025`).

---

## 3. Test Cases Specification & Verification Matrix

### 3.1 Test Case TC-E2E-001: Shared Plan Multi-Tenant Registration & Order Lifecycle
* **Test File**: [`./register_shared_plan_e2e_test.go`](./register_shared_plan_e2e_test.go)
* **Objective**: Validate asynchronous control plane registration workflow, schema-per-tenant isolation, Mailpit notification dispatch, and multi-tenant order execution.
* **Architectural Scope**: `user-service`, `tenant-service`, `infra-provisioner`, `order-service`, `notification-service`.
* **Test Procedure**:
  1. Issue HTTP request `POST /api/register` with `plan: "shared"`.
  2. Assert HTTP 202 Accepted response containing `tenant_id` (`tnt_*`).
  3. Intercept AMQP event `workspace.initiated` on exchange `company.events`.
  4. Poll `tenant_manager_db.public.tenants` until `status` transitions to `active`.
  5. Query Mailpit REST API (`http://localhost:8025/api/v1/messages`) for welcome notification.
  6. Issue HTTP request `GET /api/notifications` and assert HTTP 200 OK.
  7. Issue HTTP request `POST /api/orders` with `X-Tenant-ID` header and assert HTTP 201 Created.
  8. Issue HTTP request `GET /api/orders` with `X-Tenant-ID` header and assert order retrieval from `shared_db`.
* **Expected Result**: System executes full asynchronous lifecycle cleanly; order data isolated in schema `tenant_<slug>_order_db`.

---

### 3.2 Test Case TC-E2E-002: Dedicated Plan Dynamic Container Provisioning
* **Test File**: [`./register_dedicated_plan_e2e_test.go`](./register_dedicated_plan_e2e_test.go)
* **Objective**: Validate dynamic Docker container orchestration, health check polling, Zero-Trust role/schema bootstrapping, and private container order execution.
* **Architectural Scope**: `infra-provisioner`, `order-service`, Docker Daemon (`/var/run/docker.sock`).
* **Test Procedure**:
  1. Issue HTTP request `POST /api/register` with `plan: "dedicated"`.
  2. Assert HTTP 202 Accepted response.
  3. Verify `infra-provisioner` creates container `postgres-tenant-<id>` with 512MB RAM and 0.5 CPU limits.
  4. Poll `tenant_manager_db.public.tenants` until `status` transitions to `active`.
  5. Query routing metadata in `public.tenant_infrastructures`.
  6. Issue HTTP request `POST /api/orders` with `X-Tenant-ID` header targeting the dedicated container database.
  7. Issue HTTP request `GET /api/orders` with `X-Tenant-ID` header and verify order retrieval.
* **Expected Result**: Container provisioned, healthy, bootstrapped with domain user `order_user`, public schema privileges granted, and order data isolated on dedicated container compute.

---

### 3.3 Test Case TC-E2E-003: Inbox Deduplication & Idempotent Processing (Docs Case #2)
* **Test File**: [`./inbox_deduplication_e2e_test.go`](./inbox_deduplication_e2e_test.go)
* **Objective**: Validate at-least-once message delivery idempotency and prevention of PostgreSQL transaction abortion under duplicate AMQP message delivery.
* **Architectural Scope**: `InboxRepository`, AMQP Consumers.
* **Test Procedure**:
  1. Register a tenant and wait for activation.
  2. Publish a synthetic `tenant.order_db.ready` AMQP message with event ID `evt_duplicate_test_*`.
  3. Immediately publish a duplicate AMQP message with the exact same event ID.
  4. Query `public.inbox` for row count matching `evt_duplicate_test_*`.
* **Expected Result**: Duplicate event trapped by `ON CONFLICT (event_id) DO NOTHING`; inbox record count equals 1; zero duplicate processing side-effects.

---

### 3.4 Test Case TC-E2E-004: Fanout Exchange Broadcast & Cache Invalidation (Docs Case #13 & #14)
* **Test File**: [`./infrastructure_fanout_e2e_test.go`](./infrastructure_fanout_e2e_test.go)
* **Objective**: Validate multi-instance in-memory cache eviction (`PoolRegistry` and DSN routing metadata) upon infrastructure migration.
* **Architectural Scope**: RabbitMQ Fanout Exchange `company.events`, `order-service` replicas.
* **Test Procedure**:
  1. Register tenant, wait for activation, and issue `POST /api/orders` to populate `order-service` connection cache.
  2. Publish `tenant.infrastructure_changed` event over AMQP fanout exchange.
  3. Issue follow-up `GET /api/orders` request with `X-Tenant-ID` header.
* **Expected Result**: Event received on exclusive auto-delete queues across all replicas; local connection cache purged; subsequent requests re-resolve fresh DSN metadata cleanly.

---

### 3.5 Test Case TC-E2E-005: Container Outage Survival & Queue Catch-Up
* **Test File**: [`./service_outage_recovery_e2e_test.go`](./service_outage_recovery_e2e_test.go)
* **Objective**: Validate system fault tolerance during consumer container crashes, AMQP queue durability, and eventual consistency upon recovery.
* **Architectural Scope**: `notification-service`, RabbitMQ durable queues.
* **Test Procedure**:
  1. Stop container `notification-service` (`docker stop notification-service`).
  2. Register a new tenant via Gateway.
  3. Verify tenant reaches `active` status while `notification-service` is offline.
  4. Verify welcome email is not present in Mailpit.
  5. Restart container `notification-service` (`docker start notification-service`).
  6. Poll Mailpit REST API for welcome notification delivery.
* **Expected Result**: Events accumulate safely in persistent RabbitMQ queues; upon container startup, consumer drains queue, writes to `inbox`, and delivers email without data loss.

---

### 3.6 Test Case TC-E2E-006: Multi-Replica Scaling & Concurrency Control
* **Test File**: [`./multi_replica_scaling_e2e_test.go`](./multi_replica_scaling_e2e_test.go)
* **Objective**: Validate horizontal scaling of microservice replicas, Traefik round-robin load balancing, and outbox worker concurrency safety via `FOR UPDATE SKIP LOCKED`.
* **Architectural Scope**: Scaled `order-service` replicas, Traefik Gateway.
* **Test Procedure**:
  1. Scale `order-service` to 2 container replicas.
  2. Register tenant and wait for activation.
  3. Issue 5 concurrent order creation requests (`POST /api/orders`).
* **Expected Result**: Traffic load-balanced across replicas with 100% HTTP 201 Created success rate; zero outbox lock contention.

---

### 3.7 Test Case TC-E2E-007: Gateway Validation & Input Sanitization
* **Test File**: [`./register_e2e_test.go`](./register_e2e_test.go)
* **Objective**: Validate edge-case input rejection and gateway error handling.
* **Test Procedure**:
  1. Issue HTTP request `POST /api/register` with malformed email payload (`invalid-email-format`).
* **Expected Result**: Gateway returns HTTP 400 Bad Request before database or message broker operations occur.

---

## 4. Execution Procedures

To execute the test suite against active Docker infrastructure:

```bash
cd e2e-tests
export PATH=$PATH:/usr/local/go/bin:/opt/homebrew/bin:$HOME/go/bin
CGO_ENABLED=0 go test -v -timeout 5m ./...
```
