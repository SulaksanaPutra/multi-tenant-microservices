# Microservices Architecture & Coding Standards

This document establishes the unified mental model, architectural boundaries, code style, naming conventions, and error handling standards across all microservices in the system (`order-service`, `tenant-service`, `notification-service`, `user-service`, `infra-provisioner`).

The goal of this standardization is to provide a **consistent developer mental model**: when engineers navigate from one service to another, package layouts, constructor patterns, method names, and layer responsibilities are predictable and familiar.

---

## 1. Unified Layered Architecture & Responsibilities

Each Go microservice is structured into standard layers:

```
<service_name>/
├── cmd/                      # Application Composition Root (main.go, router.go, consumer.go)
└── internal/
    ├── domain/               # Pure Business Entities & Sentinel Errors
    ├── handler/              # HTTP Presentation Layer (Gin Controllers & Transport DTOs)
    ├── consumer/             # Event-Driven AMQP Listeners (RabbitMQ Consumers)
    ├── service/              # Application Logic Layer (Business Rules & Orchestration)
    ├── repository/           # Persistence Data Access Layer (Raw SQL / Queries)
    ├── publisher/            # AMQP Event Publishing Adapters
    ├── infrastructure/       # External Driver Adapters (Postgres, RabbitMQ, Mailer)
    ├── txcontext/            # Transaction Boundary Context Helper (replaces txctx)
    └── httputil/             # Standardized HTTP JSON Response Wrappers (replaces utils)
```

### Layer Responsibilities Matrix

| Layer / Package | Responsibilities | Allowed Dependencies | Prohibited Practices |
| :--- | :--- | :--- | :--- |
| **`domain`** | Holds pure domain model structs (`User`, `Tenant`, `Order`) and sentinel errors (`ErrNotFound`). | **None** (Stdlib only) | No framework imports, no DB drivers, no HTTP/AMQP tags. |
| **`service`** | Implements core application workflows, input validation, and business invariants. | `domain`, consumer-side interfaces | Must NOT depend on `gin.Context` or HTTP transport types. |
| **`repository`** | Executes SQL queries against PostgreSQL. Pushes domain structs into storage. | `domain`, `txcontext`, `infrastructure` | Must NOT contain transport logic or HTTP response formatting. |
| **`handler`** | Binds JSON payloads, validates transport schemas, calls application services, writes HTTP responses. | `domain`, `service` (interface), `httputil` | Must NOT write SQL queries or handle raw DB transactions directly. |
| **`consumer`** | Consumes AMQP event messages from RabbitMQ queues, delegates to domain services/provisioners. | `domain`, `service`, `infrastructure` | Must NOT perform raw SQL mutations outside service boundaries. |
| **`composition` (`cmd`)**| Instantiates concrete structs, wires dependency trees, starts servers and background workers. | All packages | Must NOT contain business logic or inline SQL queries. |

---

## 2. Constructor & Interface Standards ("Accept Interfaces, Return Structs")

### Rule 2.1: Constructors Return Concrete Pointers
Constructors MUST return concrete struct pointers rather than interface types.
```go
// GOOD
func NewUserService(repo UserRepository) *UserService
func NewTenantRepository() *TenantRepository
func NewResolver(params Params) *Resolver

// BAD
func NewUserService(repo UserRepository) UserService
```

### Rule 2.2: Consumer-Side Interface Ownership
Interfaces MUST be defined by the **consumer package** requiring the dependency, not by the provider package.
```go
// GOOD: Declared in internal/handler/user_handler.go
type UserService interface {
    GetUserByID(ctx context.Context, id string) (*domain.User, error)
    ListUsers(ctx context.Context) ([]domain.User, error)
}

// GOOD: Declared in internal/service/user_service.go
type UserRepository interface {
    GetUserByID(ctx context.Context, id string) (*domain.User, error)
    ListUsers(ctx context.Context) ([]domain.User, error)
}
```

---

## 3. Naming Conventions & Terminology

### Rule 3.1: Collection vs. Single Entity Method Naming
* **Single Entity Retrieval:** `Get<Entity>ByID(ctx, id)` or `Get<Entity>By<Field>(ctx, val)`
* **Collection / Slice Queries:** `List<Entities>(ctx, filter)` (Never use `GetOrders` or `GetUsers` for returning slices)
* **Creation / Mutations:** `Create<Entity>(ctx, input)`, `Update<Entity>(ctx, input)`, `Delete<Entity>(ctx, id)`

### Rule 3.2: Acronym & Initialism Casing
Initialisms MUST maintain consistent uppercase casing across all exported identifiers:
* `TenantID`, `UserID`, `HTTPPort`, `HTTPServer`, `DSN`, `URL`, `UUID`, `AMQP`, `RMQClient`
* **Unexported initialisms** start lowercase but keep the initialism contiguous: `tenantID`, `userID`, `httpPort`.

### Rule 3.3: Package Naming & Stutter Reduction
* **Package names** must be short, lowercase, singular nouns (`txcontext`, `httputil`, `domain`, `tenantdb`).
* **Avoid Package Stuttering:**
  ```go
  // GOOD
  tenantdb.Config
  tenantdb.Params

  // BAD
  tenantdb.TenantConfig
  tenantdb.ResolverParams
  ```
* **Eliminate Catch-all Anti-patterns:**
  Rename generic `utils` packages to `httputil` or specific domain utility packages.

---

## 4. Error Handling Taxonomy & Sentinel Errors

### Rule 4.1: Exported Sentinel Errors
Domain and service packages MUST declare exported sentinel errors for predictable error handling:
```go
var (
    ErrNotFound         = errors.New("domain: resource not found")
    ErrTenantIDRequired = errors.New("service: tenant_id is required")
    ErrInvalidInput     = errors.New("service: invalid input payload")
)
```

### Rule 4.2: HTTP Status Code Mapping
Handlers use `errors.Is(...)` to map domain errors to standard HTTP response codes:
```go
if errors.Is(err, service.ErrInvalidInput) || errors.Is(err, service.ErrTenantIDRequired) {
    httputil.WriteError(c, http.StatusBadRequest, err.Error())
    return
}
if errors.Is(err, service.ErrNotFound) {
    httputil.WriteError(c, http.StatusNotFound, err.Error())
    return
}
httputil.WriteError(c, http.StatusInternalServerError, err.Error())
```

### Rule 4.3: Error Wrapping Format
Standardize error message prefixes: `<package>: <action>: %w`
```go
return fmt.Errorf("user service: failed to create user: %w", err)
```
