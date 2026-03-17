# Zero-Trust Multi-Tenant Context Derivation and Token-Session Binding

*Comparing multi-tenant array claims against active session binding, eliminating client-side IDOR vectors, and workspace context derivation.*

---

## 1. Trade-off: Multi-Tenant Array Claims vs Active Session Binding

When architecting authorization for a multi-tenant B2B SaaS platform where a user can belong to multiple workspaces, an early design decision is token scoping:

> *Should an access token carry an array of every workspace the user has access to (`tenant_ids: ["tenant_a", "tenant_b"]`), or should each access token be strictly bound to ONE active workspace (`tenant_id: "tenant_a"`)?*

At first glance, packing all workspace memberships into one token seems convenient because it avoids token exchanges when switching organizations. In practice, array-based tenant tokens introduce several security and operational issues:

1. **Insecure Direct Object References (IDOR)**:
   If one token is valid across multiple workspaces, every API endpoint must accept a client-supplied `tenant_id` via URL query parameters or path variables (e.g. `POST /api/orders?tenant_id=xxx`). This requires every handler to cross-reference request parameters against token arrays, creating opportunities for parameter tampering bugs.
2. **Connection Pool Resolution Ambiguity**:
   In systems that resolve database connection pools dynamically per tenant, the data plane cannot determine which database pool to check out from the token alone. Relying on client-supplied parameters to route database queries creates risk of routing queries to the wrong tenant pool.
3. **Token Bloat and Coarse-Grained Revocation**:
   Embedding multiple tenant IDs, role assignments, and permission lists inflates the token payload. Furthermore, revoking a user's access in one tenant requires invalidating the entire token across all tenants.

---

## 2. Zero-Trust Token-Derived Context

To eliminate client-side parameter tampering, our system enforces **Zero-Trust Token-Derived Context**:

> **Every access token represents an active session bound to exactly ONE `tenant_id`. Downstream microservices NEVER accept `tenant_id` from client-supplied URL parameters or request bodies.**

```text
                             JWT Token Claims
                        ┌────────────────────────┐
                        │ tenant_id: "tnt_123"   │
                        └───────────┬────────────┘
                                    │
                                    ▼
                        RequireJWT Middleware
                                    │
                       (Sets: c.Set("tenantID"))
                                    │
                                    ▼
┌─────────────────────────────────────────────────────────────────────────────┐
│                       Clean Zero-Trust Tenant API                           │
├─────────────────────────────────────────────────────────────────────────────┤
│ GET /api/v1/tenants/me       ──► Resolves workspace via c.GetString()       │
│ PUT /api/v1/tenants/me       ──► Updates workspace via c.GetString()       │
│ POST /api/v1/orders          ──► Routes to tenant DB via c.GetString()     │
└─────────────────────────────────────────────────────────────────────────────┘
```

### Invariants:
- **No Client `tenant_id` Exposure**: Customer endpoints (`/api/v1/tenants/me`, `/api/v1/orders`) do not accept `tenant_id` as a client input.
- **Deterministic Extraction**: Handlers extract tenant identity strictly from verified token claims via context (`tenantID := c.GetString("tenantID")`).
- **Eliminating IDOR by Construction**: Because there is no client-supplied tenant parameter to tamper with, cross-tenant IDOR vulnerabilities are prevented at the routing layer.

---

## 3. Switching Workspaces via Token Exchange

When a user wishes to switch between workspaces:
1. The client calls `GET /api/v1/auth/workspaces` to inspect accessible workspaces.
2. To switch, the client calls `POST /api/v1/auth/switch-workspace` providing the target `tenant_id` and their refresh token.
3. `auth-service` validates that the user is an active member of the requested workspace and mints a new access token bound to the selected tenant.

---

## 4. Architectural Invariants & Operational Trade-offs

- **Zero-Trust Context Invariant**: Downstream handlers derive tenant context strictly from verified JWT claims, never from client-supplied URL query params, route parameters, or request bodies.
- **IDOR Elimination**: Binding each access token to exactly one active workspace removes parameter tampering opportunities at the routing layer.
- **Explicit Workspace Exchange**: Switching workspaces requires a dedicated token exchange call against `auth-service`, trading token re-issuance latency for clean layer isolation and small payload sizes.
