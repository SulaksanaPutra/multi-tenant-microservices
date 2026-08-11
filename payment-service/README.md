# payment-service (API / Domain Service - Payment Adapter)

`payment-service` is an **Archetype A (API / Domain Service)** microservice responsible for handling multi-tenant payment gateway integrations (Stripe, Xendit, Midtrans, Direct Bank Virtual Accounts, and Mock PSPs), webhook cryptographic verification, pessimistic concurrency locking, transaction-scoped outbox publishing, and TTL payment expiration sweeps.

---

## Architectural Bounds & Standards

- **Category:** Archetype A (API / Domain Service - Payment Adapter)
- **Layout:** `cmd/` -> `internal/{handler, service, repository, domain, provider, consumer, worker, middleware, txcontext}`
- **Responsibilities:** Multi-provider payment gateway integration (`PaymentProvider`), async payment instruction generation (`order.created` ingestion), HMAC webhook cryptographic verification, pessimistic row locking (`SELECT ... FOR UPDATE`), transaction-scoped outbox event publishing, late payment recovery (`REQUIRES_MANUAL_REVIEW`), and TTL payment expiration sweeps.

For system-wide architectural rules, layer boundaries, and unit-of-work patterns, see [Clean Architecture Standards](../docs/00-clean-architecture-standards-and-layer-hierarchy.md) and [Multi-Tenant Payment Adapter Architecture](../docs/23-how-do-we-design-a-resilient-multi-tenant-payment-adapter-with-automatic-fallback.md).

---

## Service-Specific Components & Patterns

### 1. Provider Adapter Pattern & Dynamic Tenant Resolution
- **`PaymentProvider` Interface:** Abstracted domain interface for adding, removing, or switching third-party PSP integrations (Stripe, Xendit, Midtrans, Direct Bank, Mock PSPs).
- **`TenantPSPResolver`:** Resolves per-tenant provider priority chains and encrypted credentials dynamically.

### 2. Async Instruction Generation & Transactional Outbox Pattern
- **`order.created` Ingestion:** Consumes `order.created` AMQP events to asynchronously generate multi-format payment instructions (`VIRTUAL_ACCOUNT`, `QRIS`, `REDIRECT_URL`, `DEEP_LINK`).
- **Transactional Outbox Worker:** Writes outgoing payment domain events (`payment.created`, `payment.completed`, `payment.failed`, `payment.expired`) to `payment_db.outbox` within the same Unit-of-Work database transaction, polled by `OutboxWorker` (`SELECT ... FOR UPDATE SKIP LOCKED`) and published to RabbitMQ.

### 3. Out-of-Order Webhook Concurrency Guard & Fraud Prevention
- **HMAC Signature Verification:** Verifies provider cryptographic signatures on `POST /api/payments/webhook/:provider` callbacks prior to processing.
- **Webhook Fraud Guard:** Asserts `webhook.Amount == payment.Amount && webhook.Currency == payment.Currency`. Amount mismatches trigger `FAILED_AMOUNT_MISMATCH` and halt order fulfillment.
- **Pessimistic Concurrency Guard:** Combines PostgreSQL `SELECT ... FOR UPDATE` row locking with Finite State Machine validation (`internal/domain/payment_fsm.go`) to prevent race conditions during concurrent webhook redeliveries.
- **Phantom Session Double-Billing Prevention (`payment_attempts`):** Tracks multi-attempt payment attempts to proactively cancel open remote checkout sessions when a secondary fallback attempt succeeds.

### 4. Late Payment Recovery & Expiration Sweeper
- **Late Payment Recovery (`EXPIRED` -> `REQUIRES_MANUAL_REVIEW`):** Rescuing late payments safely by flagging `REQUIRES_MANUAL_REVIEW` and emitting `payment.late_payment_received` for Ops alerting.
- **Expiration Sweeper Worker:** Background ticker worker sweeping abandoned payments past TTL (24 hours), transitioning status to `EXPIRED` and emitting `payment.expired` for inventory release.

---

## Key Interfaces & APIs

### HTTP Endpoints (Port 8086)
| Method | Endpoint | Auth | Required Scope | Description |
| :--- | :--- | :--- | :--- | :--- |
| `GET` | `/health` | None | None | Service health check |
| `POST` | `/api/payments/webhook/:provider` | None (HMAC Signature) | None | Unauthenticated PSP webhook callback |
| `GET` | `/api/payments/:id` | Bearer `<JWT>` | `payments:read` | Retrieve payment by payment ID |
| `GET` | `/api/payments/by-order/:orderID` | Bearer `<JWT>` | `payments:read` | Retrieve payment by tenant order ID |

### AMQP Published Events & Subscriptions
| Event Key | Role | Purpose / Action |
| :--- | :--- | :--- |
| `order.created` | Subscribed | Ingests new tenant orders and generates payment instructions asynchronously. |
| `payment.created` | Published | Signals payment instruction readiness. |
| `payment.completed` | Published | Signals successful payment settlement to trigger downstream order fulfillment. |
| `payment.failed` | Published | Signals payment failure or fraud validation mismatch. |
| `payment.expired` | Published | Signals payment expiration to release locked inventory. |
| `payment.late_payment_received` | Published | Alerts Ops of funds received after expiration (`REQUIRES_MANUAL_REVIEW`). |

---

## Local Development & Testing

```bash
# Run unit & repository tests
go test -v ./...

# Repomix packing for LLM analysis
npx repomix
```
