# How Do We Manage Database Transactions & Domain Invariants? Outer-Layer Unit-of-Work & Context Propagation

*An Engineering Deep Dive into Solving Service Method Composition, Transport-Agnostic Transaction Ownership, Eliminating "200 OK Before Commit" Race Conditions, and Enforcing Strict Domain Type Invariants in Go*

---

## 1. The Leaky Service Transaction & Composition Antipattern

In many Go microservice architectures, database transaction lifecycles (`*sql.DB`, `BeginTx()`, `Commit()`, `Rollback()`) are directly managed inside service methods.

```go
// ANTIPATTERN: Service method managing raw SQL transaction lifecycle
func (s *workspaceService) RegisterWorkspace(ctx context.Context, input RegisterWorkspaceInput) (*RegisterWorkspaceOutput, error) {
    tx, err := s.db.BeginTx(ctx, nil)
    if err != nil {
        return nil, err
    }
    defer tx.Rollback()

    if err := s.controlRepository.CreateTenant(ctx, tx, ...); err != nil {
        return nil, err
    }
    if err := s.outboxRepository.CreateOutboxMessage(ctx, ...); err != nil {
        return nil, err
    }
    if err := tx.Commit(); err != nil {
        return nil, err
    }

    return &RegisterWorkspaceOutput{TenantID: tenantID}, nil
}
```

### Why This Fails at Scale

1. **Leaky Abstraction & Broken Clean Architecture**: The domain/application service layer is coupled to SQL infrastructure primitives (`*sql.DB`, `*sql.Tx`). The service layer should orchestrate business logic, not manipulate database connections or transaction handles directly.
2. **The Service Composition (Nested Transaction) Problem**: If `RegisterWorkspace()` opens and commits a transaction internally, and `UpdateWorkspace()` opens and commits a transaction internally, it is **impossible to combine them into a single atomic transaction** when a new business requirement demands executing both together. Calling both methods in sequence results in two independent transactions; if the second fails, the first has already been permanently committed.
3. **Domain Invariant & Primitive Obsession Leakage**: Hardcoding untyped magic strings (`"shared"`, `"dedicated"`) and ad-hoc string slicing (`"tenant_" + uuid[:16]`) across service methods leads to subtle formatting bugs, invalid state persistence, and fragile unit testing.

---

## 2. The Flawed Fix: Framework-Coupled Handler Transactions (`*gin.Context`)

A common naive attempt to solve service composition is bringing transaction management up to the HTTP handler layer by binding a transaction handle to the HTTP framework context (`*gin.Context`):

```go
// FLOCKED PATTERN: Transaction bound to HTTP Gin context
func HandleDBTransaction(c *gin.Context, handler func(c *gin.Context) error) error {
    tx := db.Begin()
    c.Set("tx", tx)
    defer func() {
        if r := recover(); r != nil {
            tx.Rollback()
        }
    }()

    err := handler(c)
    if err != nil {
        tx.Rollback()
        return err
    }

    // ⚠️ DANGER: c.JSON() inside handler executed BEFORE tx.Commit()!
    return tx.Commit().Error
}
```

### Critical Flaws of Framework-Coupled Transactions

#### A. The "200 OK Before Commit" Race Condition
When `c.JSON(http.StatusOK, ...)` or `WriteSuccess(...)` is invoked inside the handler closure, **HTTP `200 OK` header bytes are flushed over the socket to the client immediately**. 
If `tx.Commit()` fails moments later due to a database lock timeout, serialization failure, or deferred constraint violation:
- The transaction **rolls back** in PostgreSQL.
- The client browser/mobile app **already received `200 OK`**, assuming creation succeeded.
- This introduces a silent data-corruption state where the client holds a false-positive success response for a record that does not exist in the database.

#### B. Transport Coupling (Zero Code Reuse)
HTTP Handlers are **inbound transport adapters**. Database transactions are **persistence unit-of-work boundaries**.
If transaction composition is bound to `*gin.Context`, **non-HTTP entry points cannot reuse the transaction wrapper**:
- RabbitMQ event consumers (`workspace_initiated_consumer.go`)
- Background outbox workers (`outbox_worker.go`)
- gRPC handlers or CLI maintenance scripts

None of these components have access to a `*gin.Context` without constructing complex mock framework contexts.

---

## 3. The Architectural Paradigm: Outer-Layer Unit-of-Work via `context.Context` Propagation

To achieve 100% composability without transport coupling or commit race conditions, we refactored `microservice-api` to use an **Outer-Layer `TxManager` with standard Go `context.Context` Propagation**.

```
                   ┌──────────────────────────────────────────┐
                   │    INBOUND ADAPTER (Handler / Consumer)  │
                   └────────────────────┬─────────────────────┘
                                        │
                         txManager.WithTransaction(ctx, fn)
                                        │
                                        ▼
                   ┌──────────────────────────────────────────┐
                   │          TxManager (txctx)               │
                   │    Begins *sql.Tx & injects into ctx     │
                   └────────────────────┬─────────────────────┘
                                        │ passes txCtx
                                        ▼
                   ┌──────────────────────────────────────────┐
                   │          WORKSPACE SERVICE               │
                   │   (Pure Business Action, No DB/Tx)       │
                   └────────────────────┬─────────────────────┘
                                        │ passes txCtx down
                                        ▼
                   ┌──────────────────────────────────────────┐
                   │          REPOSITORIES                    │
                   │   txctx.GetExecutor(ctx, fallback)       │
                   │   Uses *sql.Tx if present, else *sql.DB  │
                   └──────────────────────────────────────────┘
```

### Architectural Principles
1. **Transport Agnostic**: `TxManager.WithTransaction` accepts standard Go `context.Context`, making it usable in Gin Handlers, RabbitMQ Consumers, Outbox Workers, and CLI commands.
2. **Implicit Propagation**: Repositories extract `*sql.Tx` from `context.Context` via `txctx.GetExecutor(ctx, fallback)`. If a transaction is present, operations join it; if not, they execute against the default `*sql.DB` connection pool.
3. **Pure Service Layer**: Service methods become clean business building blocks. They take `context.Context` and execute repository calls without knowing or caring whether an outer transaction is active.

---

## 4. Domain Value Objects & Type Invariants (`internal/domain`)

To eliminate Primitive Obsession and magic strings, domain types and generator helpers are centralized in `internal/domain`:

```go
package domain

import (
	"fmt"
	"strings"

	"github.com/google/uuid"
)

// Plan represents the tenant hosting/pricing plan tier.
type Plan string

const (
	PlanShared    Plan = "shared"
	PlanDedicated Plan = "dedicated"
)

// IsValid checks whether the plan is an allowed plan type.
func (p Plan) IsValid() bool {
	switch p {
	case PlanShared, PlanDedicated:
		return true
	default:
		return false
	}
}

func (p Plan) String() string {
	return string(p)
}

// System entity ID prefixes.
const (
	PrefixTenant = "tenant_"
	PrefixOutbox = "outbox_"
)

// GenerateTenantID creates a standardized tenant identifier: "tenant_" + 16-char hex UUID.
func GenerateTenantID() string {
	raw := strings.ReplaceAll(uuid.New().String(), "-", "")
	return fmt.Sprintf("%s%s", PrefixTenant, raw[:16])
}

// GenerateOutboxID creates a standardized outbox event identifier: "outbox_" + 32-char hex UUID.
func GenerateOutboxID() string {
	raw := strings.ReplaceAll(uuid.New().String(), "-", "")
	return fmt.Sprintf("%s%s", PrefixOutbox, raw)
}
```

By enforcing `domain.Plan.IsValid()` and using `GenerateTenantID()` / `GenerateOutboxID()`, identifier formatting and plan validation become deterministic across all microservices.

---

## 5. Implementation Breakdown

### A. The Transport-Agnostic `TxManager` (`internal/txctx/txctx.go`)

```go
package txctx

import (
	"context"
	"database/sql"
	"fmt"
)

type execKey struct{}

// DBExecutor generalizes operations shared between *sql.DB and *sql.Tx.
type DBExecutor interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// TxManager executes operations within a database transaction boundary.
type TxManager interface {
	WithTransaction(ctx context.Context, fn func(txCtx context.Context) error) error
}

type sqlTxManager struct {
	db *sql.DB
}

func NewTxManager(db *sql.DB) TxManager {
	return &sqlTxManager{db: db}
}

func (m *sqlTxManager) WithTransaction(ctx context.Context, fn func(txCtx context.Context) error) (err error) {
	tx, err := m.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}

	txCtx := WithTx(ctx, tx)

	defer func() {
		if r := recover(); r != nil {
			_ = tx.Rollback()
			panic(r) // Preserve stack trace on panic
		} else if err != nil {
			_ = tx.Rollback()
		} else {
			err = tx.Commit()
		}
	}()

	err = fn(txCtx)
	return err
}

func WithTx(ctx context.Context, tx *sql.Tx) context.Context {
	return context.WithValue(ctx, execKey{}, DBExecutor(tx))
}

func GetExecutor(ctx context.Context, fallback DBExecutor) DBExecutor {
	if exec, ok := ctx.Value(execKey{}).(DBExecutor); ok && exec != nil {
		return exec
	}
	return fallback
}
```

### B. Dynamic Repository Executor (`internal/repository/control_plane_repository.go`)

Repositories no longer accept explicit `tx *sql.Tx` parameters. They extract the executor dynamically:

```go
func (r *controlPlaneRepository) CreateTenant(ctx context.Context, input CreateTenantInput) error {
	exec := txctx.GetExecutor(ctx, r.dbClient)
	const query = `
		INSERT INTO public.tenants (id, name, slug, owner_email, owner_name, plan, status)
		VALUES ($1, $2, $3, $4, $5, $6, 'pending');
	`
	_, err := exec.ExecContext(ctx, query,
		input.ID, input.Name, input.Slug, input.OwnerEmail, input.OwnerName, input.Plan,
	)
	return err
}
```

### C. Pure Composable Service Layer (`internal/service/workspace_service.go`)

`WorkspaceService` contains zero transaction control or `*sql.DB` handles:

```go
func (s *workspaceService) RegisterWorkspace(ctx context.Context, input RegisterWorkspaceInput) (*RegisterWorkspaceOutput, error) {
	plan := domain.Plan(strings.ToLower(input.Plan))
	if !plan.IsValid() {
		return nil, fmt.Errorf("invalid plan '%s': must be '%s' or '%s'", input.Plan, domain.PlanShared, domain.PlanDedicated)
	}

	tenantID := domain.GenerateTenantID()
	slug := utils.SanitizeSlug(input.TenantName)
	outboxID := domain.GenerateOutboxID()

	evt := publisher.WorkspaceInitiatedEvent{
		EventID:    outboxID,
		TenantID:   tenantID,
		Plan:       plan.String(),
		OwnerEmail: input.OwnerEmail,
		OwnerName:  input.OwnerName,
	}
	payloadBytes, err := json.Marshal(evt)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal WorkspaceInitiated event: %w", err)
	}

	if err := s.controlRepository.CreateTenant(ctx, repository.CreateTenantInput{
		ID:         tenantID,
		Name:       input.TenantName,
		Slug:       slug,
		OwnerEmail: input.OwnerEmail,
		OwnerName:  input.OwnerName,
		Plan:       plan.String(),
	}); err != nil {
		return nil, fmt.Errorf("failed to create tenant record: %w", err)
	}

	outboxMsg := repository.OutboxMessage{
		ID:            outboxID,
		TenantID:      &tenantID,
		AggregateType: "WORKSPACE",
		AggregateID:   tenantID,
		EventType:     "workspace.initiated",
		Payload:       payloadBytes,
	}
	if err := s.outboxRepository.CreateOutboxMessage(ctx, outboxMsg); err != nil {
		return nil, fmt.Errorf("failed to stage workspace.initiated outbox event: %w", err)
	}

	s.outboxWorker.Poke()
	return &RegisterWorkspaceOutput{TenantID: tenantID}, nil
}
```

### D. Outer Layer Transaction Ownership (`internal/handler/workspace_handler.go`)

The handler initiates the transaction boundary. HTTP `200 OK` responses are executed **only after** `WithTransaction` returns successfully:

```go
func (h *WorkspaceHandler) RegisterWorkspace(c *gin.Context) {
	var req RegisterWorkspaceRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		utils.WriteValidationError(c, err)
		return
	}

	var output *service.RegisterWorkspaceOutput

	// Outer layer controls transaction boundary!
	err := h.txManager.WithTransaction(c.Request.Context(), func(txCtx context.Context) error {
		var err error
		output, err = h.workspaceService.RegisterWorkspace(txCtx, service.RegisterWorkspaceInput{
			OwnerEmail: req.OwnerEmail,
			OwnerName:  req.OwnerName,
			Plan:       req.Plan,
			TenantName: req.TenantName,
		})
		return err
	})

	if err != nil {
		utils.WriteError(c, http.StatusInternalServerError, err.Error())
		return
	}

	// Response written ONLY after transaction successfully commits!
	utils.WriteSuccess(c, http.StatusAccepted, "Workspace registration accepted.", RegisterWorkspaceResponse{
		TenantID: output.TenantID,
	})
}
```

---

## 6. Key Engineering Takeaways & Summary Matrix

| Architectural Dimension | In-Service `sql.Tx` | Framework `*gin.Context` Tx | Outer `TxManager` + `context.Context` |
| :--- | :--- | :--- | :--- |
| **Service Composition** | Impossible (Nested `Commit` calls) | Possible in HTTP Handlers | **Fully Composable Anywhere** |
| **Commit Race Condition Protection** | Safe | Unsafe (Writes HTTP before `Commit`) | **Fully Safe (Commits before HTTP response)** |
| **Transport Independence** | Transport Agnostic | Coupled to Gin Framework | **Transport Agnostic (Gin, RabbitMQ, Workers, CLI)** |
| **Clean Architecture Alignment** | Leaks SQL drivers into Service | Leaks DB logic into HTTP Handlers | **Strict Decoupling (Domain/Service has no DB handles)** |
| **Panic & Rollback Safety** | Manual `defer tx.Rollback()` | Handled via framework recover | **Centralized `defer` in `TxManager`** |
