# Transaction Management and Domain Invariants: Outer-Layer Unit-of-Work

*Enforcing Clean Architecture boundaries, context propagation, and domain invariants in Go.*

---

## 1. The Leaky Service Transaction Antipattern

A frequent architecture mistake in Go backend services is managing database transaction lifecycles (`BeginTx()`, `Commit()`, `Rollback()`) directly inside service methods:

```go
// Antipattern: domain service directly manages database transactions
func (s *workspaceService) RegisterWorkspace(ctx context.Context, input RegisterWorkspaceInput) (*RegisterWorkspaceOutput, error) {
    tx, err := s.db.BeginTx(ctx, nil)
    if err != nil {
        return nil, err
    }
    defer tx.Rollback()

    if err := s.tenantRepo.CreateTenant(ctx, tx, ...); err != nil {
        return nil, err
    }
    if err := s.outboxRepo.CreateOutboxMessage(ctx, tx, ...); err != nil {
        return nil, err
    }
    if err := tx.Commit(); err != nil {
        return nil, err
    }
    return &RegisterWorkspaceOutput{TenantID: tenantID}, nil
}
```

### Practical Drawbacks:
1. **Prevents Service Composition**: If method A and method B each manage their own internal transactions, you cannot compose them into a single atomic business transaction. If method B fails, method A's changes are already committed.
2. **Breaks Layer Isolation**: Application services become tightly coupled to database handles (`*sql.DB`, `*sql.Tx`) rather than operating as transport- and storage-agnostic business logic.

---

## 2. Antipattern: Framework-Coupled Transactions (`*gin.Context`)

Another common workaround is attaching transaction handles to the HTTP framework context (`*gin.Context`):

```go
// Antipattern: transaction bound to web framework context
func TransactionMiddleware(db *sql.DB) gin.HandlerFunc {
    return func(c *gin.Context) {
        tx, _ := db.BeginTx(c.Request.Context(), nil)
        c.Set("tx", tx)
        c.Next()

        if len(c.Errors) > 0 {
            tx.Rollback()
            return
        }
        // Danger: c.JSON() was already written to the network socket before this commit executes
        tx.Commit()
    }
}
```

### Why Framework Middleware Fails:
- **Premature HTTP 200 Responses**: The HTTP handler writes `c.JSON(200, ...)` before middleware reaches `tx.Commit()`. If the commit fails (e.g. serialization conflict or disk error), the client has already received a success status code for data that was rolled back.
- **Coupling to HTTP**: AMQP consumers and background workers cannot use Gin middleware, forcing duplicate transaction logic across transports.

---

## 3. The Clean Architecture Solution: Outer Unit-of-Work via `TxManager`

The correct design places transaction ownership at **Layer 1** (HTTP handlers, AMQP consumers, workers) using a transport-agnostic interface:

```go
type TxManager interface {
    WithTransaction(ctx context.Context, fn func(txCtx context.Context) error) error
}
```

### Passing the Transaction Handle Transparently
Rather than passing `*sql.Tx` explicitly through every function argument, the transaction handle is stored inside `context.Context` using an unexported key:

```go
package txcontext

type contextKey struct{}

func WithTx(ctx context.Context, tx *sql.Tx) context.Context {
    return context.WithValue(ctx, contextKey{}, tx)
}

func GetExecutor(ctx context.Context, fallback DBExecutor) DBExecutor {
    if tx, ok := ctx.Value(contextKey{}).(*sql.Tx); ok && tx != nil {
        return tx
    }
    return fallback
}
```

### Handler Implementation:
```go
func (h *WorkspaceHandler) RegisterWorkspace(c *gin.Context) {
    var req RegisterRequest
    if err := c.ShouldBindJSON(&req); err != nil {
        httputil.RespondError(c, http.StatusBadRequest, err.Error())
        return
    }

    var result *domain.Workspace
    err := h.txManager.WithTransaction(c.Request.Context(), func(txCtx context.Context) error {
        var err error
        result, err = h.workspaceService.RegisterWorkspace(txCtx, req.ToInput())
        return err
    })

    if err != nil {
        httputil.RespondError(c, http.StatusInternalServerError, err.Error())
        return
    }

    // Only written to the network AFTER successful transaction commit
    c.JSON(http.StatusAccepted, result)
    h.outboxWorker.Poke()
}
```

---

## 4. Domain Invariants and Type Safety

Beyond transaction boundaries, domain models should enforce their own invariants rather than relying on untyped primitive strings:

```go
type TenantSlug struct {
    value string
}

func NewTenantSlug(raw string) (TenantSlug, error) {
    slug := strings.ToLower(strings.TrimSpace(raw))
    if len(slug) < 3 || len(slug) > 63 {
        return TenantSlug{}, errors.New("slug must be between 3 and 63 characters")
    }
    if !slugRegex.MatchString(slug) {
        return TenantSlug{}, errors.New("slug contains invalid characters")
    }
    return TenantSlug{value: slug}, nil
}

func (s TenantSlug) String() string {
    return s.value
}
```

Encapsulating validation in value object constructors prevents invalid strings from circulating through business logic and reaching database queries.

---

## 5. Architectural Invariants & Operational Trade-offs

- **Layer 1 Transaction Ownership**: Outer Unit-of-Work boundaries are owned strictly by handlers, consumers, and workers, keeping Layer 2 domain services transport-agnostic and composable.
- **Commit-Gated HTTP Responses**: HTTP responses are sent strictly after transaction commits succeed, eliminating false-positive success statuses on database failures.
- **Strongly Typed Invariants**: Primitive obsession is avoided by wrapping raw strings in validated domain value objects.
