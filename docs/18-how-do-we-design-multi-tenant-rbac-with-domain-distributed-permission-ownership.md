# How Do We Design Multi-Tenant RBAC with Domain-Distributed Permission Ownership?

*Notes on Hybrid Centralized Permission Registry, Tenant-Scoped Custom Roles, JWT Claim Enrichment, and Startup Permission Registration with Load-Shedding Resilience*

---

## 1. The Authorization Problem This Architecture Must Solve

After introducing `auth-service` as a standalone RS256 JWT issuer in Stage 1, I had a stateless identity layer but no authorization layer. Every domain service could verify *who* a caller was, but nothing enforced *what they could do*. Adding hardcoded role checks scattered across handlers would have been the obvious shortcut — and the wrong one.

The real constraints I had to satisfy simultaneously were:

1. **Zero network calls on the hot request path.** The existing `order-service` design validates JWT signatures entirely in-memory. Any permission enforcement model must not break this invariant.
2. **Domain services own their own permissions.** `order-service` knows what `orders:create` means — `auth-service` should not hardcode domain-specific knowledge.
3. **Tenant isolation.** In a B2B SaaS multi-tenant system, Tenant A's `admin` role cannot be the same entity as Tenant B's `admin` role. Roles must be tenant-scoped.
4. **Custom roles.** Tenant admins need to compose their own roles from available permissions. I cannot predict every customer's internal org structure.
5. **Near-instant permission revocation.** A terminated employee must not retain access for the full 15-minute JWT TTL window.

These five constraints ruled out naive solutions immediately.

---

## 2. Why I Rejected the Two Obvious Alternatives

### 2.1 Pattern: Per-Service Permission Stores + Fan-Out Aggregation at Login

The intuitive first approach is to let each microservice own its own `permissions` table and aggregate effective permissions at login time — `auth-service` calls each domain service to collect the user's permissions before minting the JWT.

This would have destroyed the architecture for three reasons:

- **It turns login into an N-service fan-out.** If `order-service` is down or slow, every login attempt in the entire system degrades. The hottest path in the system (authentication) becomes coupled to the availability of every domain service.
- **It inverts the dependency graph.** `auth-service` would develop compile-time knowledge of every domain service's internal API contract. Adding a new service means patching `auth-service`. This is the opposite of Clean Architecture.
- **It kills horizontal scalability.** Under a login burst (e.g., Monday morning spike), the fan-out multiplies load across every service simultaneously.

### 2.2 Pattern: Permission-Less JWT + Per-Request Introspection

The second approach is to embed only the user's role name in the JWT (`"role": "admin"`) and have each service call a central policy engine (OPA, Casbin, or `auth-service` directly) on every request to resolve whether that role grants the required permission.

This also fails:

- **Every single request carries a synchronous network dependency.** Latency, availability coupling, and connection pool exhaustion on the policy service are all inherited by every domain service for every request.
- **It's only justified when you need extremely fine-grained, runtime-mutable policies** — a complexity level far beyond what this platform needs.

The correct model is a **Hybrid: centralized registry, decentralized enforcement via JWT claims.**

---

## 3. The Architecture I Chose: Hybrid Centralized Registry with JWT Claim Projection

The design separates three concerns that I keep seeing conflated in permission system designs:

| Concern | Owner | When |
|---|---|---|
| **Permission Definition** | Each domain service | At startup, via registration |
| **Role Management** | `auth-service` DB, exposed via management API | At admin request time |
| **Authorization Enforcement** | Each domain service's middleware | At request time, from JWT claims |

`auth-service` is the **policy registry and JWT enricher**, not an orchestrator. It knows permission strings as opaque identifiers — it never needs to understand their meaning.

---

## 4. Data Model (`auth-service` DB)

### 4.1 Core Tables

```sql
-- All known permission strings across the entire platform.
-- Domain services register into this table at startup (idempotent upsert).
CREATE TABLE permissions (
    id          UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    name        TEXT        NOT NULL UNIQUE,        -- e.g. "orders:create"
    service     TEXT        NOT NULL,               -- origin service label, e.g. "order-service"
    description TEXT,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Tenant-scoped role definitions. Tenant A's "admin" is NOT the same as Tenant B's "admin".
-- Platform-operator roles use tenant_id = NULL (system-level).
CREATE TABLE roles (
    id          UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id   UUID,                               -- NULL = platform-level system role
    name        TEXT        NOT NULL,
    description TEXT,
    is_system   BOOLEAN     NOT NULL DEFAULT FALSE, -- TRUE = read-only, not managed by tenant admin
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE NULLS NOT DISTINCT (tenant_id, name)     -- Postgres 15+: NULLs are distinct by default without this
);

-- Which permissions does a role grant?
CREATE TABLE role_permissions (
    role_id       UUID NOT NULL REFERENCES roles(id) ON DELETE CASCADE,
    permission_id UUID NOT NULL REFERENCES permissions(id) ON DELETE CASCADE,
    PRIMARY KEY (role_id, permission_id)
);

-- One role per user per tenant (single role model, enforced at application layer).
-- user_id is a reference to user-service's UUID — no foreign key (cross-service boundary).
CREATE TABLE user_roles (
    user_id     UUID        NOT NULL,
    tenant_id   UUID        NOT NULL,
    role_id     UUID        NOT NULL REFERENCES roles(id) ON DELETE RESTRICT,
    assigned_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    assigned_by UUID,                               -- userID of the tenant admin who made the assignment
    PRIMARY KEY (user_id, tenant_id)                -- Enforces single-role-per-user-per-tenant
);

-- Token version counter for near-instant permission revocation.
-- Incremented when a user's role or their role's permissions change.
CREATE TABLE user_permission_versions (
    user_id    UUID        NOT NULL,
    tenant_id  UUID        NOT NULL,
    version    BIGINT      NOT NULL DEFAULT 1,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, tenant_id)
);
```

### 4.2 Design Decisions Embedded in the Schema

**Tenant-scoped roles with `UNIQUE NULLS NOT DISTINCT (tenant_id, name)`:** This constraint prevents duplicate role names within the same tenant while allowing system-level roles (`tenant_id = NULL`) to coexist. Without `NULLS NOT DISTINCT`, PostgreSQL's default NULL handling would allow multiple rows with `(NULL, "admin")` — breaking uniqueness for system roles.

**`user_roles` cross-service boundary for `user_id`:** I deliberately chose not to add a PostgreSQL `FOREIGN KEY` referencing `user-service`'s `users` table, as that table lives in a separate database (`userDB`). The integrity guarantee is maintained at the application layer: `user_id` is only written after the `user.created` event is consumed and the user-service UUID is propagated.

**`is_system` flag on roles:** This prevents tenant admins from mutating platform-defined default roles (`admin`, `viewer`, `billing_manager`) that are seeded at workspace creation. They can create custom roles freely, but system roles are read-only from their perspective.

---

## 5. JWT Claim Enrichment

I extended the JWT payload that `auth-service` mints at login and refresh time to include the resolved permission strings:

```json
{
  "sub": "user-uuid",
  "tenant_id": "tenant-uuid",
  "email": "user@example.com",
  "role": "admin",
  "permissions": ["orders:create", "orders:read", "users:manage"],
  "perm_version": 7,
  "exp": 1234567890,
  "iat": 1234566990
}
```

The `permissions` array and `perm_version` are resolved in `AuthService.Login` and `AuthService.Refresh` by joining `user_roles → roles → role_permissions → permissions`.

### 5.1 The Revocation Problem and `perm_version`

Embedding permissions in the JWT creates a staleness window: if a tenant admin revokes a permission from a role at 10:00, users with that role can continue acting on the old permission until their token expires at 10:15.

I rejected the two expensive mitigations:

- **Token blocklist (Redis):** Requires an infrastructure dependency (Redis) and a per-request cache lookup. Every domain service gains a hidden Redis dependency.
- **Reducing JWT TTL to 1-2 minutes:** Forces aggressive refresh cycles, increasing auth-service load and client complexity proportionally.

Instead, I use a **`perm_version` claim with a lightweight in-memory local check**:

```text
Flow on permission change (e.g., tenant admin removes a permission from role "editor"):
  auth-service:
    1. Update role_permissions (remove row)
    2. Increment user_permission_versions.version for all users assigned to that role
       (IN batch UPDATE; bounded by users per tenant, never a full table scan)

Flow on domain service request:
  order-service middleware:
    1. Verify JWT signature (in-memory, existing pattern) ← 0 network calls
    2. Extract: permissions[], perm_version
    3. Check in-memory VersionCache (sync.Map, keyed by userID+tenantID):
       - Cache HIT + cached_version == jwt.perm_version → proceed (< 100ns)
       - Cache MISS or cached_version != jwt.perm_version →
           GET /internal/auth/users/{userID}/perm-version (X-Internal-Service-Token)
           ← Only on version mismatch, not on every request
    4. If fetched_version > jwt.perm_version → reject 401 "token superseded"
    5. Store fetched_version in VersionCache with short TTL (e.g. 2 min)
```

This achieves **near-instant revocation** (within a single request cycle after the admin acts) at the cost of **one HTTP call per user only when their permission version changes** — not on every request. The baseline case (no permission changes in flight) runs entirely in memory.

---

## 6. Domain Permission Registration at Startup

Each domain service registers its permission strings with `auth-service` at startup before it begins serving traffic. This follows the same resilience pattern I established in `order-service`'s `TenantDBResolver`.

### 6.1 The Registration Contract

```
POST /internal/permissions/register
X-Internal-Service-Token: <HMAC token>
Content-Type: application/json

{
  "service": "order-service",
  "permissions": [
    { "name": "orders:create", "description": "Create new orders for a tenant" },
    { "name": "orders:read",   "description": "Read orders for a tenant" },
    { "name": "orders:delete", "description": "Hard-delete an order record" }
  ]
}
```

`auth-service` handles this as an `ON CONFLICT (name) DO NOTHING` upsert — calling this endpoint on every service restart is safe. Permissions are **never deleted** by this endpoint; they accumulate. If a service removes a permission, it persists in the registry (as an inert entry) until an operator explicitly removes it. This prevents a service restart from inadvertently purging a permission that a tenant admin has already assigned to a role.

### 6.2 Resilience: Semaphore + Local Cache (Mirroring `TenantDBResolver`)

When I built this, I recognised the same class of problem I solved in `order-service`'s `TenantDBResolver`: an internal HTTP call that must not block startup or goroutine workers indefinitely. I applied the same pattern:

```go
// internal/infrastructure/authclient/permission_registrar.go (in each domain service)

type PermissionRegistrar struct {
    authServiceURL       string
    internalServiceToken string
    httpSemaphore        chan struct{} // Caps concurrent registration attempts
    registered           atomic.Bool  // Guards against double-registration on reconnect
}

func (r *PermissionRegistrar) Register(ctx context.Context, permissions []Permission) error {
    // Fast path: already registered this process lifetime
    if r.registered.Load() {
        return nil
    }

    // Acquire semaphore without blocking (fail-fast, same as TenantDBResolver)
    select {
    case r.httpSemaphore <- struct{}{}:
        defer func() { <-r.httpSemaphore }()
    case <-ctx.Done():
        return ctx.Err()
    default:
        return fmt.Errorf("permission registrar: semaphore saturated, deferring registration")
    }

    // POST /internal/permissions/register
    // ... HTTP call with context timeout ...

    r.registered.Store(true)
    return nil
}
```

Key properties:
- **Fail-fast, not block:** If `auth-service` is slow at startup (e.g., still initialising), the domain service does not hang — it defers registration and retries on the next health-check tick.
- **`atomic.Bool` idempotency guard:** Prevents re-registration on reconnect events if called multiple times.
- **Context propagation:** The caller passes the service's root startup context, so registration aborts cleanly on shutdown signals.
- **No startup hard dependency:** Domain services enter a `DEGRADED` state if registration fails; they can still serve requests using their last-known JWT claims. Registration is eventually consistent, not a blocking prerequisite.

---

## 7. Authorization Enforcement in Domain Services

Enforcement is a pure middleware check on the JWT claims — zero network calls on the request path:

```go
// internal/middleware/require_permission.go (in each domain service)

// RequirePermission returns a Gin middleware that enforces a specific permission.
// It must run after RequireJWT, which sets "jwt_claims" in the Gin context.
func RequirePermission(permission string) gin.HandlerFunc {
    return func(c *gin.Context) {
        claims, ok := c.MustGet("jwt_claims").(*domain.JWTClaims)
        if !ok {
            c.AbortWithStatusJSON(http.StatusUnauthorized, httputil.ErrorResponse("unauthorized"))
            return
        }
        if !claims.HasPermission(permission) {
            c.AbortWithStatusJSON(http.StatusForbidden, httputil.ErrorResponse(
                fmt.Sprintf("missing permission: %s", permission),
            ))
            return
        }
        c.Next()
    }
}

// Route registration example:
router.POST("/api/orders",
    middleware.RequireJWT,
    middleware.RequirePermission("orders:create"),
    handler.CreateOrder,
)
```

`domain.JWTClaims.HasPermission` is a linear scan of the `permissions` slice — O(n) where n is the number of permissions in the token. Given typical permission counts per role (5–20), this is negligible. A `map[string]struct{}` pre-built from the slice could be used for roles with hundreds of permissions, but that optimisation is premature at this stage.

---

## 8. Role Management API (Tenant Admin–Facing)

The `auth-service` management API is the single administration surface for all role operations. No other service exposes role or permission management endpoints.

```
# Platform-seeded system permissions (read-only via GET)
GET  /internal/permissions                    ← X-Internal-Service-Token required

# Role CRUD (tenant admin — validated via JWT claims auth:roles:manage / auth:roles:read)
POST   /api/auth/roles                        ← Create custom role for caller's tenant
GET    /api/auth/roles                        ← List roles for caller's tenant
GET    /api/auth/roles/:id                    ← Get role details + assigned permissions
DELETE /api/auth/roles/:id                    ← Delete custom role (system roles: 403)

# Permission assignment to a role
PUT  /api/auth/roles/:id/permissions          ← Replace full permission set for a role
POST /api/auth/roles/:id/permissions          ← Add a permission to a role
DELETE /api/auth/roles/:id/permissions/:permID ← Remove a permission from a role

# User role assignment (tenant admin)
PUT  /api/auth/users/:userID/role             ← Assign a role to a user (replaces existing)
GET  /api/auth/users/:userID/role             ← Get a user's current role

# Bulk role-assignment lookup for table composition (auth:roles:read)
GET  /api/auth/users/roles?user_ids=u1,u2     ← Role assignments for many user IDs in caller's tenant
```

On every `PUT /api/roles/:id/permissions` or `DELETE /api/roles/:id/permissions/:permID`, the service atomically:
1. Updates `role_permissions`
2. Batch-increments `user_permission_versions.version` for all users with that role

This is the write side of the revocation mechanism described in Section 5.1.

---

## 9. Default Role Seeding at Workspace Creation

When `workspace.initiated` is consumed and a new tenant workspace is bootstrapped, three system roles must be seeded before the first user can log in:

```text
workspace.initiated consumed by auth-service (new consumer):
  1. Create default roles for new tenant_id:
     - "admin"   (is_system=true) → permissions: all platform permissions
     - "editor"  (is_system=true) → permissions: domain read+write, no manage
     - "viewer"  (is_system=true) → permissions: domain read-only
  2. Assign the registering user_id the "admin" role
     (user_id sourced from the workspace.initiated event payload)
  3. Seed user_permission_versions row (version=1)
```

This resolves the bootstrap gap: by the time `POST /auth/credentials/setup` is called (Step 4 of the registration flow), the user already has an `admin` role and their first JWT will carry the correct `permissions` and `perm_version` claims.

---

## 10. Row-Level (Resource-Level) Scoping & Microservice Isolation Matrix

Role-based checks answer "can this user perform this action type?" (`RequirePermission`). They don't answer "can this user perform this action on *this specific row or resource*?" The architecture enforces resource-level and tenant-level isolation directly inside each domain service's repository and handler layer using cryptographically verified JWT claims.

### 10.1 Microservice Resource Scoping & Isolation Matrix

| Microservice | Isolation Model | Enforcement Layer & File Reference | Scoping Strategy |
|---|---|---|---|
| **`order-service`** | Data-Plane Schema/Container Isolation | [`order_repository.go`](../order-service/internal/repository/order_repository.go) | Dynamic schema-per-tenant (`tenant_<slug>_order_db`) or dedicated container database connection via `TenantDBResolver`. Cross-tenant queries are physically impossible. |
| **`notification-service`** | Multi-Tenant Row-Level Query Scoping | [`notification_repository.go`](../notification-service/internal/repository/notification_repository.go) | Shared `notification_db`. Handler extracts `tenantID` from JWT claims and enforces `WHERE tenant_id = $1` in all SQL queries. |
| **`user-service`** | User-Level Resource Scoping | [`router.go`](../user-service/cmd/router.go) | `GET /users/me` extracts `userID` directly from verified JWT claims (`sub`). Users cannot read or modify other users' profiles. |
| **`tenant-service`** | Control-Plane Zero-Trust Path Scoping | [`workspace_handler.go`](../tenant-service/internal/handler/workspace_handler.go) | Control plane registry in `tenant_manager_db`. Internal routing metadata queries (`/internal/tenants/:tenant_id/...`) are protected by `X-Internal-Service-Token` and scoped by `:tenant_id`. |

### 10.2 Row-Level Code Implementation Example

In [`order_repository.go`](../order-service/internal/repository/order_repository.go):

```go
func (r *OrderRepository) ListOrders(ctx context.Context) ([]domain.Order, error) {
    // Dynamic schema scoping via tenantdb.Config resolved from verified JWT claims
    schemaName := r.config.SchemaName
    exec := txcontext.GetExecutor(ctx, r.config.DB)

    query := fmt.Sprintf(`
        SELECT id, tenant_id, customer_id, status, amount
        FROM %s.orders
        ORDER BY created_at DESC
        LIMIT 100;
    `, pq.QuoteIdentifier(schemaName))
    // ...
}
```

In [`notification_repository.go`](../notification-service/internal/repository/notification_repository.go):

```go
func (r *NotificationRepository) ListNotifications(ctx context.Context, tenantID string) ([]domain.NotificationLog, error) {
    exec := txcontext.GetExecutor(ctx, r.dbClient)
    // Row-level SQL scoping using tenantID extracted from verified JWT claims
    query := `
        SELECT id, user_id, tenant_id, description, body, status, created_at
        FROM public.notifications
        WHERE tenant_id = $1
        ORDER BY created_at DESC;
    `
    // ...
}
```

The `tenant_id` and `userID` claims from the verified JWT are used by each service to scope queries. The permission middleware validates the coarse-grained capability (`orders:read`, `notifications:read`), while the repository and handler enforce fine-grained row-level ownership and tenant isolation.

---

## 11. Architecture Topology Summary

```text
+-----------------------------------------------------------------------------------+
|                  RBAC + Domain Permission Architecture                            |
+-----------------------------------------------------------------------------------+

[ Domain Service Boot ]
      │  POST /internal/permissions/register (X-Internal-Service-Token)
      │  Idempotent upsert: { "orders:create", "orders:read", ... }
      ▼
[ auth-service ] ──► auth_db.permissions (registry of all known permission strings)

[ Tenant Admin ]
      │  POST /api/roles, PUT /api/roles/:id/permissions
      ▼
[ auth-service ] ──► auth_db.roles, role_permissions, user_roles
                      └─► Batch-increment user_permission_versions on change

[ Client Login ]
      │  POST /auth/login
      ▼
[ auth-service ]
      │  JOIN: user_roles → roles → role_permissions → permissions
      │  Mint JWT: { permissions: [...], perm_version: N, ... }
      ▼
[ Client ] ──────────────► Bearer <JWT>

[ Request to order-service ]
      │  GET /api/orders (Authorization: Bearer <JWT>)
      ▼
[ RequireJWT middleware ]
      │  In-memory RS256 signature check (0 network calls)
      ▼
[ RequirePermission("orders:read") middleware ]
      │  Check: jwt.permissions contains "orders:read"? (in-memory, O(n))
      │  Check: perm_version ↔ local VersionCache (atomic, sync.Map)
      │  On version mismatch only: GET /internal/auth/users/:id/perm-version
      ▼
[ OrderHandler → OrderService → OrderRepository ]
      │  Scope query: WHERE tenant_id = jwt.tenant_id [AND user_id = jwt.sub]
      ▼
[ Tenant DB ]
```

---

## 12. Next Feature Scope

1. **Token-Gated Workspace Invites**: Enable Tenant Admins to invite team members into an existing tenant workspace (`tenant_id`) via email invitations with specific role assignments (`admin`, `viewer`, or custom roles).
2. **OAuth 2.0 / Social Login**: Integration with Google/GitHub OAuth 2.0 Authorization Code flow, mapping OAuth claims directly onto this RS256 token issuance and permission registry pipeline.
