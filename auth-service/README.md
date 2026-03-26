# auth-service (API / Domain Service - Auth Plane)

`auth-service` is the centralized authentication authority responsible for user password setup, bcrypt credential verification, RS256 JWT access token signing, opaque refresh token rotation, multi-tenant custom role management, and central RBAC permission registration.

---

## Architectural Bounds & Standards

- **Category:** Archetype A (API / Domain Service - Auth Plane)
- **Layout:** `cmd/` -> `internal/{handler, service, repository, domain, crypto, middleware}`
- **Responsibilities:** User credential store (`auth_db`), asymmetric RS256 JWT signing, refresh token revocation, single-use setup tokens, central role/permission management, and domain permission registration.

For system-wide architectural rules, layer boundaries, and unit-of-work patterns, see [Clean Architecture Standards](../docs/00-clean-architecture-standards-and-layer-hierarchy.md).

---

## Service-Specific Components & Patterns

### 1. Asymmetric RS256 JWT & Token Security
- **Asymmetric Signing:** Signs short-lived access tokens (15-min TTL) using RS256 RSA private keys. Publishes RSA public key via `/.well-known/jwks.json` so domain services verify tokens in-memory with zero network calls.
- **Refresh Token Rotation:** Issues 7-day opaque refresh tokens. Persists SHA-256 hashes of refresh tokens in `auth_db.refresh_tokens` for instant revocation on logout.
- **Password Setup Link:** Generates single-use setup tokens for newly provisioned workspace users (`POST /internal/auth/setup-token`). Setup token SHA-256 hashes are stored in `auth_db.password_setup_tokens` until consumed via `POST /api/auth/credentials/setup`.

### 2. Unified Identity & Workspace Selection
- **Global Identity per Email:** `user_credentials` holds exactly **one row per email** (the `tenant_id` column was intentionally removed). A user's tenant memberships live in `user_tenant_memberships (user_id, tenant_id)`, so a single identity can belong to many workspaces.
- **Login Branching:** `POST /api/auth/login` resolves memberships via `GetUserMemberships`. Every successful login (one *or* more memberships) returns `status: "SELECT_WORKSPACE"`, a single-use exchange token (10-min TTL), and the workspace list. The list is **enriched best-effort** with `tenant_name`, `tenant_slug`, and `tenant_plan` fetched (zero-trust) from `GET /internal/tenants/:id/profile` on tenant-service; a lookup failure falls back to `tenant_id`-only so login never depends on it. The client (web UI) auto-exchanges the token silently for a single workspace, or shows a workspace-selection modal for multiple.
- **Explicit Workspace Selection:** The client completes login by calling `POST /api/auth/select-tenant` with the exchange token + chosen `tenant_id`; auth-service verifies the requested tenant is one of the user's memberships, validates/marks the token used, and mints a JWT strictly scoped to that tenant.
- **Tenant-Bound Refresh Tokens:** `refresh_tokens` stores the `tenant_id` active at issuance, so `POST /api/auth/refresh` preserves the workspace context instead of minting a tenant-less token.

### 2. Multi-Tenant RBAC & Instant Revocation
- **Domain Permission Registration:** Domain services register their required permissions (e.g. `orders:create`, `orders:read`, `users:roles:manage`) with `auth-service` upon startup via `POST /internal/permissions/register`. `auth-service` acts as an opaque central policy registry.
- **Tenant-Scoped Custom Roles:** Enforces `UNIQUE (tenant_id, name)` and protects system default roles (`admin`, `viewer`).
- **Instant Revocation via Permission Version Bumping:** Updating role permissions or assigning roles to users batch-increments `user_permission_versions.version` in `auth_db`, instantly invalidating cached JWT permissions across microservices.

---

## Key Interfaces & APIs

### HTTP Endpoints (Port 8085)
| Method | Endpoint | Auth | Description |
| :--- | :--- | :--- | :--- |
| `POST` | `/api/auth/credentials/setup` | None | Consume single-use setup token & set user password → return JWT pair |
| `POST` | `/api/auth/login` | None | Authenticate email/password → always returns `SELECT_WORKSPACE` with a single-use exchange token + workspace list (enriched with `tenant_name`/`tenant_slug`/`tenant_plan`); client exchanges it for the JWT pair |
| `POST` | `/api/auth/select-tenant` | None | Exchange a login exchange token for a JWT pair bound to a selected member workspace |
| `POST` | `/api/auth/refresh` | None | Rotate refresh token → issue new RS256 JWT (preserves tenant context) |
| `POST` | `/api/auth/logout` | JWT Bearer | Revoke refresh token |
| `GET` | `/api/auth/permissions` | JWT Bearer | List catalog of registered system permissions |
| `POST` | `/api/auth/roles` | JWT Bearer | Create tenant-scoped custom role |
| `GET` | `/api/auth/roles` | JWT Bearer | List available roles for tenant |
| `GET` | `/api/auth/roles/:id` | JWT Bearer | Retrieve specific role details |
| `PUT` | `/api/auth/roles/:id/permissions` | JWT Bearer | Update permissions linked to role |
| `DELETE` | `/api/auth/roles/:id` | JWT Bearer | Delete custom role |
| `PUT` | `/api/auth/users/:userID/role` | JWT Bearer | Assign role to tenant user |
| `GET` | `/api/auth/users/:userID/role` | JWT Bearer | Retrieve user role assignment |
| `POST` | `/internal/auth/setup-token` | `X-Internal-Service-Token` | Internal endpoint for setup token generation |
| `POST` | `/internal/auth/permissions/register` | `X-Internal-Service-Token` | Internal domain permission startup registration |
| `GET` | `/internal/auth/users/:userID/perm-version` | `X-Internal-Service-Token` | Internal query for user permission version |
| `GET` | `/.well-known/jwks.json` | None | RSA public key for downstream JWT signature verification |
| `GET` | `/health` | None | Health check endpoint |

---

## Local Development & Setup

```bash
# Generate RSA Key Pair for Local Development
openssl genrsa -out private.pem 2048
openssl rsa -in private.pem -pubout -out public.pem

# Populate AUTH_JWT_PRIVATE_KEY_PEM in .env
# Run unit & repository tests
go test -v ./...

# Repomix packing for LLM analysis
npx repomix
```
