# How Do We Design User-Tenant Session Binding and Zero-Trust Token Context Derivation?

*Notes on 1-to-1 Active Session Binding vs Multi-Tenant Claims Bloat, Eliminating Client-Side `tenant_id` IDOR Vulnerabilities, and Future Multi-Workspace Evolution Without Breaking Downstream Microservices*

---

## 1. The Dilemma: Multi-Tenant Array Claims vs Active Session Binding

When I architected the authentication and multi-tenant authorization subsystem in [docs/17-how-do-we-isolate-authentication-and-jwt-token-issuance-standalone-auth-service.md](17-how-do-we-isolate-authentication-and-jwt-token-issuance-standalone-auth-service.md) and [docs/18-how-do-we-design-multi-tenant-rbac-with-domain-distributed-permission-ownership.md](18-how-do-we-design-multi-tenant-rbac-with-domain-distributed-permission-ownership.md), I confronted a fundamental multi-tenant domain question:

> *"In an enterprise B2B SaaS platform, a single human user (email) can belong to multiple workspace tenants. Should their JWT access token embed an array of all accessible tenant IDs (`tenant_ids: ["tnt_a", "tnt_b"]`), or should every JWT access token be bound to exactly ONE active tenant workspace session (`tenant_id: "tnt_a"`)?"*

At first glance, embedding an array of tenant IDs into a single JWT payload appears attractive—it allows a user to query endpoints across multiple tenants using a single static access token without re-authenticating.

However, based on my experience, **multi-tenant array tokens fundamentally break Zero-Trust security and Clean Architecture isolation**:

### 1. Insecure Direct Object Reference (IDOR) & Parameter Tampering Surface
If a single JWT access token grants authorization for 10 distinct tenants, every downstream HTTP request must carry an explicit, client-supplied URL path parameter or query parameter (e.g., `PUT /api/tenants?tenant_id=tnt_victim`). 

This forces downstream microservices to accept tenant identifiers from untrusted client input, creating an **IDOR attack vector**. A malicious actor could attempt to modify `tenant_id` query parameters in their browser/API client to target a victim tenant's data. Backend handlers are forced to write redundant, error-prone equality checks comparing token claims against URL query parameters.

### 2. Failure of Connection Pool Isolation
In our multi-tenant data plane, `order-service` and `tenant-service` dynamically resolve database connections per tenant (Database-per-Tenant for Dedicated Plan containers, or Schema-per-Tenant on Shared Plan PostgreSQL instances). 

If a JWT token contains an array of tenant IDs rather than a single active tenant binding, connection pool resolvers (`tenantdb.Resolver`) cannot infer the target connection safely from the token payload alone. The system is forced to rely on unverified client parameters to route database queries, introducing risk of cross-tenant connection pool leaks.

### 3. Payload Bloat and Invalidation Complexity
Embedding multiple tenant memberships, per-tenant role assignments, and per-tenant permission sets into a single JWT payload inflates the token payload size exponentially. Furthermore, revoking a user's access in *one* tenant would require invalidating their token across *all* tenants, severely complicating the instant permission revocation model detailed in [docs/19-how-do-we-achieve-instant-jwt-revocation-with-perm-version-caching.md](19-how-do-we-achieve-instant-jwt-revocation-with-perm-version-caching.md).

---

## 2. The Solution: Zero-Trust Token-Derived Context (`/api/tenants/me`)

To eliminate IDOR attack vectors and enforce absolute multi-tenant boundary isolation, I established the principle of **Zero-Trust Token-Derived Context**:

> **Every RS256 JWT access token represents an active session bound to exactly ONE `tenant_id`. Downstream microservices NEVER accept `tenant_id` from client-supplied URL path parameters or query parameters for active workspace operations.**

```text
                                  JWT Token Claims
                             ┌────────────────────────┐
                             │ tenant_id: "tnt_123"   │
                             └───────────┬────────────┘
                                         │
                                         ▼
                             RequireJWT Middleware
                                         │
                            (Injects: c.Set("tenantID"))
                                         │
                                         ▼
 ┌─────────────────────────────────────────────────────────────────────────────┐
 │                       Clean Zero-Trust Tenant API                           │
 ├─────────────────────────────────────────────────────────────────────────────┤
 │ GET /api/tenants/me       ──► Fetches workspace for c.GetString("tenantID") │
 │ PUT /api/tenants/me       ──► Updates workspace for c.GetString("tenantID") │
 │ PUT /api/tenants/me/plan  ──► Changes plan for c.GetString("tenantID")      │
 └─────────────────────────────────────────────────────────────────────────────┘
```

### Key Architectural Invariants:
1. **Zero Client-Side `tenant_id` Parameter Exposure**:
   Endpoints like `GET /api/tenants/me`, `PUT /api/tenants/me`, `PUT /api/tenants/me/plan`, `POST /api/orders`, and `PUT /api/users/me` do **NOT** accept `tenant_id` in URL parameters or request bodies.
2. **Deterministic Context Extraction**:
   Downstream HTTP handlers extract the active tenant context strictly via `tenantID := c.GetString("tenantID")` set by the `RequireJWT` middleware after verifying the RS256 RSA signature.
3. **Mathematically Impossible IDOR**:
   Because client parameters for `tenant_id` are completely banished, parameter tampering attacks are mathematically impossible. An attacker cannot alter a URL parameter to gain unauthorized visibility into another tenant's workspace.

---

## 3. Trade-Off Analysis

| Architectural Dimension | Option A: Multi-Tenant Token (`tenant_ids: [...]`) | Option B: Active Session Token (`tenant_id: "tnt_1"`) [Chosen] |
| :--- | :--- | :--- |
| **Security & IDOR Surface** | High (Client passes `tenant_id` in URL; risk of cross-tenant parameter tampering) | **Zero (Token-derived; client parameter tampering is impossible)** |
| **Connection Pooling Isolation** | Complex (Must inspect untrusted URL params to select DB pool) | **Deterministic (Token dictates DB pool directly)** |
| **Token Payload Footprint** | Unbounded growth ($O(N)$ tenants $\times$ permissions) | **Fixed & Bounded ($<800$ bytes)** |
| **Revocation Complexity** | High ($O(N)$ tenant version invalidation checks) | **Low (Single `perm_version` check in `VersionCache`)** |
| **Workspace Switching UX** | Implicit (No new token required) | **Explicit (`POST /api/auth/switch-tenant` issues new active session token)** |

---

## 4. Future Evolution: Multi-Workspace Switching Without Breaking Architecture

If business requirements evolve to allow a single human user (email) to belong to multiple tenants, **how do we implement workspace switching without breaking our existing architecture or modifying downstream microservices?**

The beauty of 1-to-1 Active Session Token Binding is that it is **100% backward-compatible**. We can extend `auth-service` to support multi-tenant user memberships without altering a single line of code in `order-service`, `user-service`, `tenant-service`, `notification-service`, or any downstream `RequireJWT` middleware.

### 1. Persistence Layer Evolution (`auth-service`)
We introduce a `user_tenant_memberships` table in `auth_db`:

```sql
CREATE TABLE public.user_tenant_memberships (
    id VARCHAR(64) PRIMARY KEY,
    user_id VARCHAR(64) NOT NULL,
    tenant_id VARCHAR(64) NOT NULL,
    role_id VARCHAR(64) NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT uk_user_tenant UNIQUE (user_id, tenant_id)
);
```

### 2. Multi-Workspace Authentication Flow
When a user logs in via `POST /api/auth/login`:
- For **any** successful login (1 *or* multiple memberships), `auth-service` returns a HTTP 200 response carrying a single-use `exchange_token` and a workspace selection list. The client exchanges the token via `POST /api/auth/select-tenant` to receive an RS256 JWT bound to the chosen `tenant_id` (exchanged silently when only one workspace exists):
  ```json
  {
    "status": 200,
    "message": "Multiple workspaces available",
    "data": {
      "workspaces": [
        { "tenant_id": "tnt_acme", "tenant_name": "Acme Corp", "role": "admin" },
        { "tenant_id": "tnt_globex", "tenant_name": "Globex Inc", "role": "viewer" }
      ]
    }
  }
  ```

### 3. Explicit Workspace Switching (`POST /api/auth/switch-tenant`)
To switch active workspaces, the client issues a request to `auth-service`:

```http
POST /api/auth/switch-tenant
Content-Type: application/json
Authorization: Bearer <current_jwt_token>

{
  "target_tenant_id": "tnt_globex"
}
```

- `auth-service` verifies that the authenticated `user_id` holds an active membership record in `user_tenant_memberships` for `tnt_globex`.
- `auth-service` mints a **new RS256 JWT access token** with claims scoped strictly to `tenant_id: "tnt_globex"` and the role/permissions granted in that target tenant.
- The client replaces its stored JWT access token in memory with the new token.

### 4. Zero Downstream Breaking Changes Guarantee
Because the newly issued token contains `tenant_id: "tnt_globex"`, all downstream microservices (`order-service`, `user-service`, `tenant-service`, `notification-service`) continue to operate seamlessly:
- Connection pool resolvers (`tenantdb.Resolver`) route database queries to `tnt_globex`'s isolated database/schema.
- `/api/tenants/me`, `/api/users/me`, and `/api/orders` retrieve resources for `tnt_globex` automatically.
- **Zero code changes** are required across any downstream handler, service, repository, or middleware layer.
