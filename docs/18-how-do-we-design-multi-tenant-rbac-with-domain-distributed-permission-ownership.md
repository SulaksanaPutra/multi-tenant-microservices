# Multi-Tenant RBAC with Domain-Distributed Permission Ownership

*Centralized permission registries, tenant-scoped custom roles, and decentralized in-memory JWT enforcement.*

---

## 1. System Requirements for Multi-Tenant RBAC

Designing authorization across microservices in a B2B SaaS platform introduces specific constraints:

1. **Zero Network Calls on Hot Paths**: Domain services (`order-service`) must enforce permissions in memory without calling an authorization service on every HTTP request.
2. **Domain-Distributed Permission Ownership**: `order-service` knows what `orders:create` means. `auth-service` should not have hardcoded domain knowledge about order business logic.
3. **Tenant-Scoped Roles**: A role named `Manager` in Tenant A must be independent from `Manager` in Tenant B. Customers must be able to configure custom roles within their workspaces.
4. **Fast Revocation**: Revoking permissions must take effect quickly without waiting for long token expirations.

---

## 2. Architectural Model: Hybrid Registry with JWT Claim Projection

We separate permission concerns into three distinct areas:

| Concern | Responsible Service | Lifecycle |
|---|---|---|
| **Permission Definition** | Domain services (`order-service`) | Startup registration via API |
| **Role & Assignment Storage** | `auth-service` database | Admin management API |
| **Enforcement** | Domain service middleware | Request execution via JWT claims |

`auth-service` functions as a **policy registry and token enricher**. It treats permission strings as opaque identifiers (e.g. `"orders:create"`), remaining agnostic to what the permission actually authorizes.

---

## 3. Data Schema in `auth-service`

```sql
-- Platform-wide registry of all available permissions
CREATE TABLE permissions (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    code        VARCHAR(100) NOT NULL UNIQUE, -- e.g. "orders:create"
    service     VARCHAR(50)  NOT NULL,        -- e.g. "order-service"
    description TEXT,
    created_at  TIMESTAMPTZ  NOT NULL DEFAULT NOW()
);

-- Tenant-scoped roles
CREATE TABLE roles (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id   VARCHAR(64)  NOT NULL,
    name        VARCHAR(100) NOT NULL,
    description TEXT,
    is_system   BOOLEAN      NOT NULL DEFAULT FALSE,
    created_at  TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    UNIQUE(tenant_id, name)
);

-- Role-to-permission mappings
CREATE TABLE role_permissions (
    role_id       UUID REFERENCES roles(id) ON DELETE CASCADE,
    permission_id UUID REFERENCES permissions(id) ON DELETE CASCADE,
    PRIMARY KEY (role_id, permission_id)
);

-- User-to-role assignment within a tenant
CREATE TABLE user_roles (
    user_id    VARCHAR(64) NOT NULL,
    tenant_id  VARCHAR(64) NOT NULL,
    role_id    UUID REFERENCES roles(id) ON DELETE CASCADE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (user_id, tenant_id, role_id)
);
```

---

## 4. Permission Registration and In-Memory Enforcement

### Startup Registration
When `order-service` boots, it idempotently registers its supported permissions with `auth-service`:

```go
perms := []authclient.Permission{
    {Code: "orders:create", Description: "Create new customer orders"},
    {Code: "orders:read",   Description: "View order details"},
}
_ = authClient.RegisterPermissions(ctx, perms)
```

### In-Memory Enforcement Middleware
During login, `auth-service` queries the user's role permissions for the selected tenant and embeds them as a string slice inside the JWT (`"permissions": ["orders:create", "orders:read"]`).

In `order-service`, authorization runs entirely in memory:

```go
func RequirePermission(perm string) gin.HandlerFunc {
    return func(c *gin.Context) {
        claims, exists := c.Get("claims")
        if !exists {
            c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
            return
        }

        userClaims := claims.(*TokenClaims)
        for _, p := range userClaims.Permissions {
            if p == perm || p == "admin:*" {
                c.Next()
                return
            }
        }

        c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "insufficient permissions"})
    }
}
```

---

## 5. Architectural Invariants & Operational Trade-offs

- **Domain-Distributed Authority**: Domain services declare and own their permission strings at startup; `auth-service` stores and assigns them without requiring hardcoded domain logic.
- **Zero-Network Hot-Path Authorization**: Role permissions are projected into JWT claims upon token minting, enabling instantaneous in-memory permission checks in domain handlers.
- **Tenant Isolation of Roles**: Custom roles and role-permission mappings are strictly scoped by `tenant_id` to prevent cross-tenant permission pollution.
