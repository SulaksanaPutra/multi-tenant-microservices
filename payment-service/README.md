# `payment-service` — Payment Gateway Integration Microservice

> **Archetype A:** API / Domain Service  
> **Port:** `8086`  
> **Database:** `payment_db` (PostgreSQL)  
> **Documentation:** [docs/23-how-do-we-design-a-resilient-multi-tenant-payment-adapter-with-automatic-fallback.md](../docs/23-how-do-we-design-a-resilient-multi-tenant-payment-adapter-with-automatic-fallback.md)

---

## Overview

`payment-service` is an **Archetype A (API / Domain Service)** microservice responsible for handling multi-tenant payment gateway integrations (Stripe, Xendit, Midtrans, Direct Bank Virtual Accounts, and Mock PSPs).

It encapsulates payment provider transports, async payment instruction generation (Virtual Accounts, QRIS, Redirect URLs), multi-provider fallback execution, webhook cryptographic HMAC signature verification, pessimistic concurrency locking (`SELECT ... FOR UPDATE`), and TTL payment expiration sweeps.

---

## Architectural Highlights

- **Provider Adapter Pattern (`PaymentProvider`):** Abstracted domain interface for adding, removing, or switching third-party PSP integrations.
- **Async Payment Instructions:** Generates multi-format payment instructions (`VIRTUAL_ACCOUNT`, `QRIS`, `REDIRECT_URL`, `DEEP_LINK`) asynchronously upon `order.created` event ingestion.
- **Phantom Session Double-Billing Prevention (`payment_attempts`):** Multi-attempt tracking table that proactively cancels open remote checkout sessions when a fallback provider attempt succeeds.
- **Webhook Security & Fraud Guard:** Asserts `webhook.Amount == payment.Amount && webhook.Currency == payment.Currency`. Amount mismatches trigger `FAILED_AMOUNT_MISMATCH` and halt order fulfillment.
- **Out-of-Order Webhook Concurrency Guard:** PostgreSQL `SELECT ... FOR UPDATE` row locking combined with Finite State Machine validation (`internal/domain/payment_fsm.go`).
- **Late Payment Recovery (`EXPIRED` $\rightarrow$ `REQUIRES_MANUAL_REVIEW`):** Rescuing late payments safely by flagging `REQUIRES_MANUAL_REVIEW` and emitting `payment.late_payment_received` for Ops alerting.
- **Expiration Sweeper Worker:** Background ticker worker sweeping abandoned payments past TTL (24 hours), transitioning status to `EXPIRED` and emitting `payment.expired` for inventory release.
- **`TenantPSPResolver`:** Dynamic per-tenant provider chains and encrypted credentials resolution.

---

## REST Endpoints & Permissions

| Method | Endpoint | Authorization | Permission Required | Description |
| :--- | :--- | :--- | :--- | :--- |
| `GET` | `/health` | None | None | Service health check |
| `POST` | `/api/payments/webhook/:provider` | None (HMAC Signature) | None | Unauthenticated PSP webhook callback |
| `GET` | `/api/payments/:id` | Bearer JWT | `payments:read` | Retrieve payment by ID |
| `GET` | `/api/payments/by-order/:orderID` | Bearer JWT | `payments:read` | Retrieve payment by tenant order ID |

---

## Directory Layout

```text
payment-service/
├── cmd/main.go                        # Port 8086, HTTP Router, AMQP Consumers, Outbox & Sweeper Workers
├── migrations/
│   ├── 00001_init_payment_schema.sql  # Goose migrations
│   └── embed.go                       # Embedded migration filesystem
└── internal/
    ├── consumer/
    │   └── order_created_consumer.go  # Listens to order.created AMQP queue
    ├── domain/                        # Pure entities, sentinel errors, FSM, and Provider interfaces
    ├── handler/                       # HTTP Controllers
    ├── httputil/                      # Standardized response utilities
    ├── infrastructure/                # AuthClient PermissionRegistrar & AMQP Driver
    ├── middleware/                    # RS256 RequireJWT & RequirePermission RBAC
    ├── migration/                     # Goose library-mode migration runner
    ├── provider/                      # ProviderRegistry, CircuitBreaker, MockProvider & DirectBankProvider
    ├── repository/                    # PostgreSQL repositories (payments, attempts, inbox, outbox)
    ├── service/                       # PaymentService core domain logic
    ├── txcontext/                     # Unit-of-work transaction manager
    └── worker/                        # ExpirationSweeper & OutboxWorker
```
