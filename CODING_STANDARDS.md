# Microservices Architecture & Coding Standards

This document establishes the unified mental model, architectural boundaries, code style, naming conventions, and error handling standards across all microservices in the system (`order-service`, `auth-service`, `tenant-service`, `notification-service`, `user-service`, `infra-provisioner`).

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
    ├── migration/            # goose Library-Mode Migrations (Control-Plane Schema)
    ├── txcontext/            # Transaction Boundary Context Helper (replaces txctx)
    └── httputil/             # Standardized HTTP JSON Response Wrappers (replaces utils)
```

> **Service-Specific Layout Exceptions:** Archetype B (`notification-service`) omits `handler/`/`repository/` where it has no HTTP/persistence surface; Archetype C (`infra-provisioner`) omits `handler/` entirely; `order-service` is the single exception that keeps a bespoke `MigrationService` in `internal/service` for runtime per-tenant DDL provisioning (see Rule 7.2).

### Layer Responsibilities Matrix

| Layer / Package | Responsibilities | Allowed Dependencies | Prohibited Practices |
| :--- | :--- | :--- | :--- |
| **`domain`** | Holds pure domain model structs (`User`, `Tenant`, `Order`) and sentinel errors (`ErrNotFound`). | **None** (Stdlib only) | No framework imports, no DB drivers, no HTTP/AMQP tags. |
| **`service`** | Implements core application workflows, input validation, and business invariants. | `domain`, consumer-side interfaces | Must NOT depend on `gin.Context` or HTTP transport types. |
| **`repository`** | Executes SQL queries against PostgreSQL. Pushes domain structs into storage. | `domain`, `txcontext`, `infrastructure` | Must NOT contain transport logic or HTTP response formatting. |
| **`handler`** | Binds JSON payloads, validates transport schemas, calls application services, writes HTTP responses. | `domain`, `service` (interface), `httputil` | Must NOT write SQL queries or handle raw DB transactions directly. |
| **`consumer`** | Consumes AMQP event messages from RabbitMQ queues, delegates to domain services/provisioners. | `domain`, `service`, `infrastructure` | Must NOT perform raw SQL mutations outside service boundaries. |
| **`migration`** | Applies embedded goose SQL migrations against the control-plane DB at boot (versioned, advisory-locked). | stdlib (`database/sql`), goose, `migrations/` embed | Must NOT contain business logic or runtime per-tenant DDL (see Rule 7.2). |
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
Interfaces MUST be defined by the **consumer package** requiring the dependency, not by the provider package. This applies universally to repositories, services, mailers, workers, and publisher adapters.
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

// GOOD: Declared in internal/service/notification_service.go
type Mailer interface {
    SendWelcomeEmail(recipientEmail, tenantID string) (string, string, error)
}

// GOOD: Declared in internal/worker/outbox_worker.go
type TenantEventPublisher interface {
    PublishWorkspaceInitiated(ctx context.Context, evt domain.WorkspaceInitiatedEvent) error
    PublishWorkspaceReady(ctx context.Context, evt domain.WorkspaceReadyEvent) error
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

### Rule 3.4: Full-Word Layer Variable & Field Naming (No Layer Abbreviations)
Initialized variables, struct fields, constructor parameters, and interface declarations MUST use **full, explicit layer words** rather than truncated abbreviations:
* **Repository Layer:** Use `userRepository`, `outboxRepository`, `controlPlaneRepository`, `inboxRepository`, `notificationRepository`, `orderRepository` *(PROHIBITED: `repo`, `Repo`, `Repository`, `r`)*.
* **Service Layer:** Use `userService`, `workspaceService`, `notificationService`, `orderService`, `migrationService` *(PROHIBITED: `svc`, `userSvc`, `workspaceSvc`)*.
* **Publisher Layer:** Use `publisher`, `tenantEventPublisher`, `userEventPublisher` *(PROHIBITED: `pub`)*.
* **Consumer Layer:** Use `workspaceInitiatedConsumer`, `userCreatedConsumer`, `tenantReadyConsumer` *(PROHIBITED: `cons`)*.
* **Handler Layer:** Use `workspaceHandler`, `orderHandler`, `notificationHandler` *(PROHIBITED: `hnd`, `h`)*.
* **Migration Layer:** Use `migration.Run(ctx, db)` at the composition root; keep the goose entrypoint in `internal/migration` *(PROHIBITED: inline DDL in `cmd/migration.go`, `mig`)*.

---

## 4. Error Handling Taxonomy & Sentinel Errors

### Rule 4.1: Exported Sentinel Errors in `internal/domain`

Sentinel errors MUST be declared **exclusively in the pure `domain` package** (Domain Purity — see the Layer Responsibilities Matrix). Service, handler, repository, consumer, and infrastructure packages MUST NOT declare their own `Err*` sentinels; they reference `domain.Err*` for cross-cutting error contract checks.

```go
// GOOD — declared in internal/domain/errors.go
var (
    ErrNotFound         = errors.New("domain: resource not found")
    ErrTenantIDRequired = errors.New("auth service: tenant_id is required")
    ErrInvalidInput     = errors.New("auth service: invalid input payload")
)

// PROHIBITED — sentinels declared in internal/service/*.go, internal/handler/*.go, etc.
var ErrUserNotFound = errors.New("service: user not found") // ← VIOLATION
```

> **Rationale:** The domain package is the single source of truth for business invariants and can be imported by every layer without creating dependency cycles. Application services remain transport-agnostic and only wrap/`errors.Is` against `domain.Err*`.

### Rule 4.2: HTTP Status Code Mapping
Handlers use `errors.Is(...)` to map domain sentinel errors to standard HTTP response codes:
```go
if errors.Is(err, domain.ErrInvalidInput) || errors.Is(err, domain.ErrTenantIDRequired) {
    httputil.WriteError(c, http.StatusBadRequest, err.Error())
    return
}
if errors.Is(err, domain.ErrNotFound) {
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

### Rule 4.4: Repository Error Translation Responsibility
Layer 3 repositories MUST translate all database-driver-specific errors into domain sentinel errors before returning to the caller. Layer 2 services MUST NOT import `database/sql` or check for `sql.ErrNoRows` directly.

```go
// CORRECT — in repository (Layer 3)
if errors.Is(err, sql.ErrNoRows) {
    return nil, fmt.Errorf("tenant '%s': %w", id, domain.ErrNotFound)
}

// CORRECT — in service (Layer 2)
if errors.Is(err, domain.ErrNotFound) {
    return fmt.Errorf("%w: %s", domain.ErrTenantNotFound, id)
}

// PROHIBITED — in service (Layer 2)
import "database/sql"
if errors.Is(err, sql.ErrNoRows) { ... } // ← VIOLATION: driver primitive in Layer 2
```

---

## 5. Event-Driven Messaging Standards (AMQP & RabbitMQ)

### Rule 5.1: Centralized Event Definitions in `domain/events.go`
* All AMQP exchanges, routing keys, queue names, and event payload structs MUST be explicitly declared in `internal/domain/events.go`.
* **PROHIBITED:** Declaring package-level exchange names or routing key constants inside individual `consumer/` or `publisher/` files. Centralizing definitions in `domain` eliminates implicit package-scope magic and ensures explicit, predictable imports across packages.

### Rule 5.2: Separation of Consumers & Publishers (Single Responsibility Principle)
* **Consumers (`internal/consumer`):** AMQP consumers MUST strictly handle inbound message consumption, ACK/NACK channel management, and delegating work to application services. Consumers MUST NOT directly construct raw AMQP payload frames or publish outbound events.
* **Publishers (`internal/publisher`):** Outbound event broadcasting MUST be encapsulated inside dedicated publisher adapters (`internal/publisher`). Consumers requiring outbound message dispatch MUST accept a publisher interface dependency via constructor injection.

### Rule 5.3: AMQP Topology Alignment (Competing Consumer vs. Fanout Broadcast)
* **Named Competing Consumer Queues:** Used for single-worker task execution (e.g., DDL migrations, user profile creation) where an event must be processed **exactly once** by a single microservice replica.
* **Exclusive Anonymous Fanout Queues:** Used for real-time state synchronization and cache invalidation (`tenant.infrastructure_changed`) where an event must be broadcast to **all live microservice replicas simultaneously**.

### Rule 5.4: No External I/O Inside Transaction Boundaries
`txManager.WithTransaction` closures MUST contain **only DB operations**. SMTP, HTTP, gRPC, and any other external network calls are **prohibited** inside a `WithTransaction` closure.

**Rationale:** While the closure executes, a DB connection and potentially row-level locks are held open. A 30-second SMTP timeout translates directly into a 30-second held DB connection. If the external call fails inside the transaction, the rollback also undoes the DB writes — the consumer NACK causes a retry, which may produce duplicate side effects.

**Correct Pattern:** Execute DB work inside the transaction, capture the result, then perform external I/O after the transaction commits:
```go
// CORRECT — DB inside tx, I/O after commit
var details *DispatchDetails
txManager.WithTransaction(ctx, func(txCtx context.Context) error {
    var err error
    details, err = service.DoDBWork(txCtx, ...)
    return err
})
mailer.Send(details.Email) // outside tx

// PROHIBITED — network I/O inside the transaction closure
txManager.WithTransaction(ctx, func(txCtx context.Context) error {
    service.DoDBWork(txCtx, ...)
    mailer.Send(...) // ← VIOLATION
    return nil
})
```

### Rule 5.5: Barrier Sync Consumer Pattern
When a consumer must wait for **N independent events** before triggering an action (barrier sync), both the inbox guard and the barrier state read are **Layer 1 consumer responsibilities**. The business service receives the collected events as a plain data argument — it must NOT hold an `InboxRepository` dependency.

Canonical structure:
```go
// Layer 1 Consumer (handleDelivery)
txManager.WithTransaction(ctx, func(txCtx context.Context) error {
    isDup, _ := inboxService.ClaimEvent(txCtx, inboxInput)   // 1. Guard
    if isDup { return nil }
    events, _ := inboxService.GetBarrierEvents(txCtx, tenantID) // 2. Barrier read
    details, err = businessService.Process(txCtx, input, events) // 3. DB write only
    return err
})
// Phase 2: External I/O after commit
if details != nil {
    mailer.SendWelcomeEmail(details.RecipientEmail, details.TenantID)
}
```

---

## 6. DTO, Domain Entity & Database Naming Standards

### Rule 6.1: Layer-by-Layer DTO Suffix Conventions

To maintain strict Clean Architecture boundaries and avoid transport coupling, each layer MUST follow these DTO suffix conventions:

| Layer / Package | Incoming Struct Naming | Outgoing Struct Naming | Primary Responsibility |
| :--- | :--- | :--- | :--- |
| **`internal/handler`** | `{Action}{Entity}Request`<br>*(e.g. `RegisterWorkspaceRequest`)* | `{Action}{Entity}Response`<br>*(e.g. `RegisterWorkspaceResponse`)* | Holds HTTP transport rules, `json:"..."` struct tags, and Gin validation tags (`binding:"required"`). |
| **`internal/service`** | `{UseCase}Input`<br>*(e.g. `RegisterWorkspaceInput`)* | `{UseCase}Output`<br>*(e.g. `RegisterWorkspaceOutput`)* | Transport-agnostic business logic inputs & outputs. Must NOT contain `json` tags. |
| **`internal/consumer`** | `domain.{EventName}Event`<br>*(e.g. `domain.WorkspaceInitiatedEvent`)* | N/A *(ACK / NACK)* | Unmarshals raw AMQP bytes into Domain Event, maps to Service `{UseCase}Input`. |
| **`internal/publisher`** | `domain.{EventName}Event`<br>*(e.g. `domain.UserCreatedEvent`)* | Raw AMQP Payload | Serializes Domain Event payload and publishes to AMQP exchange. |
| **`internal/repository`** | `{Action}{Entity}Input`<br>*(e.g. `CreateTenantInput`, `CreateUserInput`)* | `domain.{Entity}`<br>*(e.g. `domain.Tenant`)* | Operation-specific DB write parameters (`Input`) vs. pure Domain Entities (`Output`). |
| **`internal/domain`** | Pure Domain Entities & Integration Events | Pure Domain Entities & Integration Events | Single source of truth for business entities (`Tenant`, `User`, `Order`) and events (`WorkspaceReadyEvent`). |

> [!MANDATORY]
> **Repository DTO Input Rule:** All repository write/mutation methods (`Create*`, `Update*`, `Upsert*`, `Save*`) MUST accept dedicated operation-specific DTO structs (`{Action}{Entity}Input`) defined inside the repository package. Repository write methods are FORBIDDEN from accepting raw `domain.{Entity}` structs directly.

### Rule 6.2: DB Table to Domain Entity Singularization Rule

* Domain entities inside `internal/domain` MUST represent the singular form of their underlying database table:
  - Table **`public.tenants`** ──> Entity `domain.Tenant`
  - Table **`public.tenant_infrastructures`** ──> Entity `domain.TenantInfra`
  - Table **`public.users`** ──> Entity `domain.User`
  - Table **`public.orders`** ──> Entity `domain.Order`
  - Table **`public.notifications`** ──> Entity `domain.NotificationLog`
  - Table **`public.inbox`** ──> Entity `domain.InboxMessage`
  - Table **`public.outbox`** ──> Entity `domain.OutboxMessage`
* **PROHIBITED:** Suffixing domain entity structs with `Record` (e.g. use `Tenant` instead of `TenantRecord`).
* **Cognitive Collision Prevention Rule:** If a DB table's plural name (e.g. `tenant_services`) singularizes to an Application Service name (`TenantService`), the table MUST be named after its domain intent (e.g. `tenant_infrastructures` ──> `domain.TenantInfra`) to prevent mental model collisions between application services and database entities.

### Rule 6.3: Internal Inter-Service vs. Public API Explicit Naming Rule

To eliminate cognitive confusion between public user-facing operations and internal control-plane/inter-service operations:

* **Presentation Layer (`internal/handler`)**:
  - Handlers processing internal inter-service endpoints (authenticated via `InternalAuthMiddleware` or `X-Internal-Service-Token`) MUST use the `internal_` filename prefix and the `Internal` struct prefix:
    - `internal_auth_handler.go` ──> `InternalAuthHandler`
    - `internal_permission_handler.go` ──> `InternalPermissionHandler`
    - `internal_tenant_handler.go` ──> `InternalTenantHandler`
  - Transport DTOs for internal handlers MUST be prefixed with `Internal`:
    - `Internal{Action}Request` (e.g. `InternalCreateSetupTokenRequest`, `InternalRegisterPermissionsRequest`)
    - `Internal{Action}Response` (e.g. `InternalCreateSetupTokenResponse`, `InternalPermissionVersionResponse`, `InternalGetInfrastructureResponse`)
* **Application Use-Case Layer (`internal/service`)**:
  - Application services handling internal inter-service use cases MUST use the `internal_` filename prefix and the `Internal` struct prefix:
    - `internal_auth_service.go` ──> `InternalAuthService`
    - `internal_permission_service.go` ──> `InternalPermissionService`
  - Service DTOs for internal services MUST be prefixed with `Internal`:
    - `Internal{UseCase}Input` (e.g. `InternalCreateSetupTokenInput`, `InternalRegisterPermissionsInput`)
    - `Internal{UseCase}Output` (e.g. `InternalCreateSetupTokenOutput`, `InternalRoutingOutput`)
* **Persistence Layer (`internal/repository`)**:
  - Repositories persist domain entities into PostgreSQL. Database storage mechanisms do NOT have transport scope concepts; repositories MUST maintain standard entity names (`RoleRepository`, `PermissionRepository`) without the `Internal` prefix.

---

## 7. Database Migration Standards

All microservice schemas MUST be managed as **versioned SQL files**. Raw DDL MUST NOT be embedded as inline strings in Go source files. The platform uses a **two-tier migration strategy**:

| Tier | Owner | Mechanism | Applies to |
| :--- | :--- | :--- | :--- |
| **Control-Plane Schema** | `auth-service`, `user-service`, `tenant-service`, `notification-service` | goose library-mode, embedded via `//go:embed`, applied at boot by `internal/migration.Run(ctx, db)` | The service's own database (`auth_db`, `user_db`, `tenant_manager_db`, `notification_db`) |
| **Data-Plane Per-Tenant DDL** | `order-service` | Bespoke `internal/service.MigrationService` (`MigrateTenantDB`) | Arbitrary, runtime-derived tenant DSNs (shared schemas & dedicated DB containers) |

### Rule 7.1: Versioned SQL Migration Files

* **Control-plane services** own their schema under `<service_name>/migrations/` as ordered goose files: `NNNNN_<description>.sql`.
  - `auth-service/migrations/00001_init_auth_schema.sql`
  - `auth-service/migrations/00002_auth_inbox.sql`
  - `tenant-service/migrations/00001_init_tenant_manager_schema.sql`
* **Data-plane service** (`order-service`) keeps its ordered idempotent file: `order-service/migrations/001_create_orders.sql`.
* Migrations MUST be **idempotent** (`CREATE TABLE IF NOT EXISTS`, `CREATE INDEX IF NOT EXISTS`) so they safely converge both fresh databases and warm databases bootstrapped by an older `infrastructure/init.sql`.
* **PROHIBITED:** Inline DDL string literals in Go files (e.g. `cmd/migration.go`), ad-hoc `CREATE TABLE` in handler/service code, or schema defined anywhere outside `migrations/`.

### Rule 7.2: Control-Plane Loading via `internal/migration` (goose) + Intentional Data-Plane Exception

* **Control-plane services** load and apply their migration files through a thin `internal/migration` package exposing:
  * `Run(ctx context.Context, db *sql.DB) error` — calls `goose.SetBaseFS(migrations.FS)`, sets the `postgres` dialect, and applies all pending `NNNNN_*.sql` migrations.
  * The SQL files are compiled into the binary via `//go:embed` in `migrations/embed.go`; the runtime container needs no sidecar migration tool.
  * goose records applied versions in `goose_db_version` and acquires a PostgreSQL advisory lock so concurrent replica boots cannot stampede migrations.
* The composition root (`cmd/main.go`) MUST invoke `migration.Run(...)` at startup **before** repositories are instantiated.
* **Intentional Exception — `order-service` `MigrationService`:** `order-service` alone keeps the bespoke `internal/service.MigrationService` (`NewMigrationService`, `NewMigrationServiceFromSQL`, `MigrateTenantDB`). This is **deliberate, not drift**: it provisions schemas against *arbitrary, runtime-derived tenant DSNs* that cannot be known at boot, supports `{{SCHEMA_NAME}}` placeholder substitution, executes DDL outside the static goose version table, and supports non-transactional migrations (`-- tx: false`). goose (boot-time, one database) cannot express this workflow.
* The `internal/migration` package (and `order-service`'s `MigrationService`) is the single sanctioned exception to **Rule 4.4** (services MUST NOT import `database/sql`): executing raw schema DDL is its only responsibility.

### Rule 7.3: Relationship with `infrastructure/init.sql`

* `infrastructure/init.sql` is the one-shot container bootstrap that creates **databases only** (`user_db`, `auth_db`, `tenant_manager_db`, `notification_db`) on a fresh PostgreSQL volume (`/docker-entrypoint-initdb.d`). It no longer creates service tables.
* Per-service `migrations/*.sql` are the **authoritative schema source**: they create base tables on fresh volumes and converge warm databases that were bootstrapped by a pre-slim `init.sql`.
* Per-service migrations are the per-service source of truth; `init.sql` must only ever add/remove databases, never tables.

### Rule 7.4: Per-Tenant Schema Provisioning (Data Plane)

* Schemas provisioned per-tenant (e.g. `order-service` shared plan) MUST use `{{SCHEMA_NAME}}` placeholders in the migration file and be executed through the tenant-provisioning path (`order-service` consumer/service via `MigrationService.MigrateTenantDB`), NOT at service startup.

---

## 8. Unit Test Coverage Standards

### Rule 8.1: Every Package MUST Ship Unit Tests

Every **new package / module** added to any microservice MUST include a `*_test.go` file covering its exported surface. A package merged without tests is incomplete.

* **New service package:** unit tests for `New*` constructors, business-rule branches, and sentinel-error contracts (mock the repository via a consumer-side interface).
* **New repository package:** unit tests using the service-local `internal/testutil.MockDBExecutor` / `MockResult` mocks to assert query text, argument binding, and error translation — no real database required.
* **New consumer package:** unit tests with mock `TxManager`, `InboxService`, and domain-service interfaces covering ACK / NACK / requeue / DLQ routing decisions (including duplicate-event and poison-pill paths).
* **New migration / infrastructure package:** unit tests that do NOT require external services — e.g. structural checks over the embedded migration FS (expected files, goose `Up`/`Down` annotations, unique ordered version prefixes), nil-channel guards, and context-cancellation paths.
* **New domain file:** struct/JSON round-trip tests and contract tests that pin event payload shapes shared across services (e.g. `UserCreatedEvent` must decode the publisher's exact JSON).

### Rule 8.2: Test Command

All service tests run with CGO disabled on macOS (avoids `dyld`/`LC_UUID` issues):

```bash
CGO_ENABLED=0 go test -v ./...   # inside the specific service directory
```


