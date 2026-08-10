# Resilient Multi-Tenant Payment Adapters and Automatic Fallbacks

*Isolating payment providers, handling provider outages with fallback chains, regional instructions, and webhook concurrency.*

---

## 1. Architectural Requirements for Multi-Tenant Payments

Integrating payment processors (such as Stripe, Midtrans, or Xendit) into a multi-tenant microservices architecture presents specific operational challenges:

1. **Layer Separation**: Payment gateway SDKs and vendor-specific webhook structures must not leak into core `order-service` domain logic.
2. **Provider Downtime & Fallbacks**: If a primary gateway (e.g. Stripe) encounters an outage or rejects a card, the system must support falling back to a secondary provider (e.g. Midtrans) without double-charging the customer.
3. **Diverse Payment Instructions**: Payment methods differ across regions: global cards return checkout URLs (`payment_url`), while Southeast Asian payment methods return Virtual Account numbers (`va_number`) or QRIS barcodes. Storage schemas must handle diverse payment instruction structures.
4. **Concurrent Webhook Ingestion**: Webhooks can arrive out of order, duplicated, or in parallel. Handling webhooks requires strict concurrency control to prevent duplicate order fulfillments.

To address these requirements, payment orchestration is managed by a dedicated service: `payment-service`.

---

## 2. End-to-End Payment Topology

```text
[ Client App ] ──► 1. POST /api/v1/orders ──► [ order-service ]
                                                    │
                                                    ├─► Saves Order (Status: PENDING_PAYMENT)
                                                    └─► Outbox: order.created
                                                               │
                                                               ▼
                                                      [ payment-service ]
                                                               │
                                                               ├─► 1. Records Payment (Status: PENDING)
                                                               ├─► 2. Resolves tenant provider credentials
                                                               ├─► 3. Executes fallback chain:
                                                               │      Attempt Primary (Stripe) -> Timeout
                                                               │      Attempt Fallback (Midtrans) -> Success
                                                               └─► 4. Saves payment instructions (VA/QR/URL)
                                                                      and marks PAYMENT_INSTRUCTIONS_READY
```

---

## 3. Automatic Provider Fallback Execution

When generating payment instructions, `payment-service` executes a resilient fallback chain:

```go
func (e *FallbackExecutor) Execute(ctx context.Context, chain []Provider, req PaymentRequest) (*PaymentResponse, error) {
    var lastErr error
    for _, provider := range chain {
        resp, err := provider.CreatePayment(ctx, req)
        if err == nil {
            return resp, nil
        }
        lastErr = err
        log.Printf("Payment provider %s failed: %v. Attempting next provider...", provider.Name(), err)
    }
    return nil, fmt.Errorf("all payment providers in chain failed: %w", lastErr)
}
```

---

## 4. Webhook Concurrency and State Verification

When a payment provider notifies the system of a completed transaction via webhook:

1. **HMAC Signature Verification**: Validates the webhook payload against the tenant's provider webhook secret.
2. **Pessimistic Row Locking**:
   ```sql
   SELECT id, status, amount FROM payments WHERE id = $1 FOR UPDATE;
   ```
   Locks the payment record in PostgreSQL, serializing any concurrent webhooks for the same order.
3. **Inbox Deduplication**: Ensures duplicate webhook IDs sent by the provider are processed exactly once.
4. **Amount Matching**: Verifies the webhook amount equals the recorded order amount before transitioning state.
5. **State Transition Validation**: Enforces that transitions move validly (`PENDING` -> `SUCCEEDED`). Late-arriving webhooks on expired payments are flagged for manual review rather than silently accepted.

---

## 5. Architectural Invariants & Operational Trade-offs

- **Domain Isolation Invariant**: Payment gateway SDKs and vendor-specific payloads are encapsulated in `payment-service` and never leak into `order-service`.
- **Pessimistic Webhook Concurrency**: Concurrent webhooks are serialized using `SELECT ... FOR UPDATE` and deduplicated via the transactional inbox pattern to guarantee single execution.
- **Fallback Chain Discipline**: Provider timeouts trigger automatic fallback to secondary adapters without double-charging or corrupting payment state.
