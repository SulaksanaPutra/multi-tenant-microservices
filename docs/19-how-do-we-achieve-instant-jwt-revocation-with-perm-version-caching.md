# Near-Instant JWT Revocation with Permission Versioning

*Balancing stateless RS256 token verification with rapid permission revocation and local memory caches.*

---

## 1. The Revocation Dilemma

Stateless RS256 JWT tokens provide strong performance: downstream services verify access tokens in memory using the public key, requiring zero database lookups or inter-service network calls.

However, statelessness introduces a trade-off: **What happens when a user is demoted, their permissions change, or their account is suspended?**

If tokens are purely stateless, a compromised or demoted user retains access until their token expires (e.g., 15 minutes). For security-sensitive applications, waiting 15 minutes for access revocation is unacceptable. Conversely, validating every request against `auth-service` via synchronous HTTP or database lookups re-introduces a single point of failure and adds latency (+15ms to +50ms per request).

We resolve this using **Token Versioning (`perm_version`) backed by a lightweight in-memory cache**.

---

## 2. The Token Payload Size Question

A common concern with embedding permissions in JWT tokens is payload size.

In our multi-tenant architecture, a user typically holds 10 to 25 permission strings for their active workspace.

Let's check the byte size:
```json
{
  "sub": "usr_99812a3f",
  "tenant_id": "ten_7712bc9e",
  "email": "user@example.com",
  "permissions": [
    "orders:create", "orders:read", "orders:delete",
    "users:read", "users:update", "tenants:read"
  ],
  "perm_version": 4,
  "iss": "auth-service",
  "exp": 1770000000
}
```

- Permission array: 15 permissions * 15 bytes/string = ~225 bytes.
- Total JSON payload: ~380 bytes.
- Signed Base64URL JWT string: ~800 bytes.

Standard web servers (Traefik, Nginx, Go `net/http`) allow HTTP header sizes of 8 KB to 16 KB. An 800-byte token uses less than 10% of standard header limits while avoiding database lookups on 99.9% of requests.

---

## 3. How Token Versioning Works

### 1. Version Tracking in Database
`auth-service` tracks an integer version for each user within a tenant:

```sql
CREATE TABLE user_permission_versions (
    user_id     VARCHAR(64) NOT NULL,
    tenant_id   VARCHAR(64) NOT NULL,
    version     BIGINT NOT NULL DEFAULT 1,
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (user_id, tenant_id)
);
```

### 2. Token Minting
When minting an access token, `auth-service` queries the user's current version and embeds it as `perm_version: N`.

### 3. Revocation / Invalidation Trigger
Whenever an admin updates a user's role or revokes permissions, `auth-service` executes:
```sql
UPDATE user_permission_versions 
SET version = version + 1, updated_at = NOW() 
WHERE user_id = $1 AND tenant_id = $2;
```
It also broadcasts a lightweight invalidation event (`user.permissions_revoked`) over RabbitMQ containing `user_id`, `tenant_id`, and the new version number.

### 4. Downstream In-Memory Enforcement
Downstream services maintain a local cache (`VersionCache`) updated by the RabbitMQ broadcast:
- When a request arrives, middleware checks if the token's `perm_version` is less than the cached version for `(user_id, tenant_id)`.
- If `token.perm_version < cached_version`, the token is rejected immediately with `401 Unauthorized`.
- The user's client must use their refresh token to obtain an updated access token reflecting their new permissions.

---

## 4. Architectural Invariants & Operational Trade-offs

- **Sub-Millisecond Revocation Window**: Permission changes increment a version counter in PostgreSQL and broadcast invalidation events over AMQP, updating local replica caches within milliseconds.
- **Payload Overhead vs Performance**: Embedding permissions and version counters in JWTs adds ~380 bytes per token, trading negligible bandwidth overhead for complete elimination of database verification queries on normal traffic.
- **Fail-Safe Token Refresh**: Clients rejected due to outdated `perm_version` must execute a refresh flow to obtain current permissions, avoiding cascading service errors.
