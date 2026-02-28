# How Do We Achieve Instant Revocation in Stateless RS256 JWTs via Version Caching?

*Notes on Token Payload Bloat vs Zero Network Calls, Version Claims (`perm_version`), Local VersionCache Mechanics, and Revocation SLAs*

---

## 1. The Dilemma: Pure Stateless JWT Expiry vs Real-Time Security Revocation

When I designed `auth-service` as a standalone RS256 JWT issuer in [docs/17-how-do-we-isolate-authentication-and-jwt-token-issuance-standalone-auth-service.md](17-how-do-we-isolate-authentication-and-jwt-token-issuance-standalone-auth-service.md), I established a core performance invariant: **downstream domain services (`order-service`, `user-service`, `notification-service`) verify incoming JWT access tokens entirely in memory using the RSA Public Key with zero network round-trips to `auth-service`.**

However, pure stateless JWTs create a critical security vulnerability: **What happens when a user's permissions are revoked or a compromised employee is demoted?**

If tokens are purely stateless and downstream services only verify the signature and expiration time (`exp = 15m`), the demoted user retains full administrative access for up to 15 minutes until their access token expires. For enterprise multi-tenant B2B SaaS platforms, a 15-minute window for unauthorized administrative access is unacceptable.

On the other hand, checking every incoming request against `auth-service` via internal HTTP calls (`GET /internal/auth/verify`) destroys performance:
- `auth-service` becomes a **Single Point of Failure (SPOF)**.
- Every API request incurs an extra $+15\text{ms}$ to $+50\text{ms}$ HTTP network latency.
- `auth_db` experiences connection pool exhaustion under high traffic bursts.

This document details how we resolved this conflict using **Token Versioning (`perm_version`) with Local In-Memory `VersionCache` Enforcement**.

---

## 2. Addressing the Myth of "JWT Permission Bloat"

Before explaining the versioning mechanics, we must address a common engineering objection: *"Does embedding a user's permission array inside the JWT payload cause token bloat?"*

### The Engineering Math:
In a multi-tenant B2B SaaS architecture, a tenant user is assigned a single role per workspace (see [docs/18-how-do-we-design-multi-tenant-rbac-with-domain-distributed-permission-ownership.md](18-how-do-we-design-multi-tenant-rbac-with-domain-distributed-permission-ownership.md)). That role grants 10 to 30 permission strings.

Let's compute the exact byte footprint:

```json
{
  "sub": "usr_99812a3f",
  "tenant_id": "ten_7712bc9e",
  "email": "admin@acme.com",
  "permissions": [
    "orders:create", "orders:read", "orders:delete",
    "users:read", "users:update", "tenants:read"
  ],
  "perm_version": 4,
  "iss": "auth-service",
  "exp": 1770000000
}
```

1. **Permission Array Size:** 15 permissions $\times$ 15 bytes/string $\approx$ **225 bytes**.
2. **Total JSON Payload:** ~380 bytes.
3. **Signed RS256 JWT String (Base64URL):** **~750 to 850 bytes**.

### Comparison Against HTTP Header Limits:
Standard web servers, reverse proxies, and API gateways (Traefik, Nginx, Envoy, Go `net/http`) enforce an HTTP header limit of **8 KB to 16 KB** (8,192 to 16,384 bytes).

An 800-byte JWT payload consumes **less than 10% of the standard HTTP header limit**. The bandwidth overhead is completely negligible compared to the massive performance gain of performing **zero network calls on 99.99% of API requests**.

---

## 3. How Token Versioning (`perm_version`) Works

The `perm_version` claim bridges the gap between stateless JWT validation and real-time access revocation.

### 3.1 Where Is the Version Stored & How Is It Scoped?
`auth-service` maintains the version state in PostgreSQL inside `auth_db`:

```sql
CREATE TABLE user_permission_versions (
    user_id     VARCHAR(64) NOT NULL,
    tenant_id   VARCHAR(64) NOT NULL,
    version     BIGINT NOT NULL DEFAULT 1,
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (user_id, tenant_id)
);
```

- **Storage Location:** Centralized in `auth_db` (`auth-service`).
- **Scoping:** It is scoped **per user per tenant** via composite Primary Key `(user_id, tenant_id)`. A single user could belong to Tenant A with `version = 1` and Tenant B with `version = 3`.

---

### 3.2 The Initial Seeding Lifecycle (When Is Version Created?)
1. **Initial Seeding:** When a user completes password setup (`POST /auth/credentials/setup`) or is assigned a role in a workspace, `auth-service` inserts an initial record:
   ```sql
   INSERT INTO user_permission_versions (user_id, tenant_id, version) 
   VALUES ('usr_123', 'ten_abc', 1) 
   ON CONFLICT (user_id, tenant_id) DO NOTHING;
   ```
2. **Token Minting:** During `POST /auth/login` or `POST /auth/refresh`, `auth-service` reads `version = 1` from `user_permission_versions` and embeds it directly inside the minted RS256 JWT access token:
   ```json
   { "sub": "usr_123", "tenant_id": "ten_abc", "permissions": ["orders:create"], "perm_version": 1 }
   ```

---

### 3.3 Two-Tier Cache Synchronization: AMQP Broadcast Push + HTTP Pull Fallback
The `VersionCache` population in `order-service` follows the **exact same Two-Tier Synchronization Topology** as `TenantDBResolver` (see [docs/13-how-do-we-prevent-horizontal-split-brain-cache-invalidation-and-multi-tenant-connection-sprawl.md](13-how-do-we-prevent-horizontal-split-brain-cache-invalidation-and-multi-tenant-connection-sprawl.md) and [docs/14-how-do-we-prevent-transient-network-split-brain-cache-invalidation-loss.md](14-how-do-we-prevent-transient-network-split-brain-cache-invalidation-loss.md)):

```text
+-----------------------------------------------------------------------------------+
|               Two-Tier VersionCache Invalidation Topology                         |
+-----------------------------------------------------------------------------------+

Tier 1: Real-Time Push Invalidation (< 50ms)
[ Tenant Admin ] ──► Edits Role Permissions ──► [ auth-service :8085 ]
                                                       │
                                                       ├─► 1. UPDATE user_permission_versions (version = 2)
                                                       │
                                                       └─► 2. Publish AMQP Event to Topic Exchange:
                                                              user.permissions_updated
                                                              Routing Key: user.permissions_updated
                                                              Payload: { user_id: "usr_123", perm_version: 2 }
                                                              │
                                                              ▼
                                                   [ RabbitMQ Topic Exchange ]
                                                              │
                                    ┌─────────────────────────┼─────────────────────────┐
                                    ▼                         ▼                         ▼
                         [ Exclusive Queue 1 ]     [ Exclusive Queue 2 ]     [ Exclusive Queue 3 ]
                                    │                         │                         │
                                    ▼                         ▼                         ▼
                         [ order-replica-1 ]       [ order-replica-2 ]       [ order-replica-3 ]
                         VersionCache.Set("usr_123", 2) VersionCache.Set(...)    VersionCache.Set(...)

Tier 2: Lazy Pull Fallback & Bounded Staleness (60s TTL)
[ Client Request (JWT: perm_version=1) ]
       │
       ▼
 [ order-replica-1 ]
       │ 1. Check local VersionCache["usr_123"]
       ├─► Cache Hit & Version Match (1 == 1): Proceed (0 Network Calls, < 100ns)
       ├─► Cache Miss or TTL Expired (60s):
       │     Call auth-service: GET /internal/auth/users/usr_123/perm-version
       │     Header: X-Internal-Service-Token
       │     Update VersionCache["usr_123"] = 2
       └─► Version Mismatch (JWT=1 < Cached=2):
             REJECT IMMEDIATELY -> HTTP 401 Unauthorized (Token Superseded)
```

1. **Tier 1 — Real-Time Push Invalidation (AMQP Broadcast):**
   When `auth-service` increments a user's version in `auth_db`, it broadcasts a `user.permissions_updated` event over the `company.events` Topic Exchange to exclusive, auto-delete queues bound by every domain service replica. All instances (`order-service-replica-1`, `2`, `3`) consume this event in real-time and execute `VersionCache.Set(userID, newVersion)` within **< 50ms**.

2. **Tier 2 — Lazy Pull & Network Split-Brain Resilience:**
   If RabbitMQ is temporarily unreachable or a network partition drops the AMQP invalidation frame, **the local `VersionCache` 60-second TTL acts as a hard safety boundary**. On cache expiry, the next API request triggers a fallback HTTP call (`GET /internal/auth/users/:userID/perm-version`), guaranteeing a maximum bounded staleness SLA of **$\le$ 60 seconds** even during total message broker failure.

---

## 4. The Invalidation Flow: Step-by-Step Sequence

```text
Step 1: Admin Modifies Role / Revokes Access
[ Tenant Admin ] ──► PUT /api/roles/:id/permissions ──► [ auth-service :8085 ]
                                                              │
                                                              ├─► 1. Update auth_db.role_permissions
                                                              ├─► 2. UPDATE user_permission_versions SET version = version + 1
                                                              └─► 3. Broadcast AMQP: user.permissions_updated

Step 2: Downstream Request Execution
[ Client Request (JWT: perm_version=1) ]
       │
       ▼
 [ order-service :8084 ]
       │
       ├─► 1. RequireJWT: RS256 Signature Verification in RAM (< 1 µs)
       ├─► 2. RequirePermission("orders:create"): Claims check in RAM
       │
       ├─► 3. Version Check (Local VersionCache):
       │      ├─► Local Cache Hit & claims.perm_version (1) == cached_version (1):
       │      │   PROCEED IMMEDIATELY (0 Network Calls, < 100 ns)
       │      └─► Version Mismatch (JWT perm_version 1 < Cached Version 2):
       │          REJECT IMMEDIATELY -> HTTP 401 Unauthorized (Token Superseded)
       ▼
[ Client Application ] ──► Receives 401 ──► Calls POST /auth/refresh ──► Obtains new JWT (perm_version=2)
```
