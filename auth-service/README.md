# auth-service (API / Domain Service - Auth Plane)

`auth-service` is the centralized authentication authority responsible for user password setup, bcrypt credential verification, RS256 JWT access token signing, opaque refresh token rotation, and central RBAC permission registration.

---

## Architectural Bounds & Standards

- **Category:** Archetype A (API / Domain Service - Auth Plane)
- **Layout:** `cmd/` -> `internal/{handler, service, repository, domain, crypto, middleware}`
- **Responsibilities:** User credential store (`auth_db`), asymmetric RS256 JWT signing, refresh token revocation, single-use setup tokens, and domain permission registration.

For system-wide architectural rules, layer boundaries, and unit-of-work patterns, see [Clean Architecture Standards](../docs/00-clean-architecture-standards-and-layer-hierarchy.md).

---

## Service-Specific Components & Patterns

### 1. Asymmetric RS256 JWT & Token Security
- **Asymmetric Signing:** Signs short-lived access tokens (15-min TTL) using RS256 RSA private keys. Publishes RSA public key via `/.well-known/jwks.json` so domain services verify tokens in-memory with zero network calls.
- **Refresh Token Rotation:** Issues 7-day opaque refresh tokens. Persists SHA-256 hashes of refresh tokens in `auth_db.refresh_tokens` for instant revocation on logout.
- **Password Setup Link:** Generates single-use setup tokens for newly provisioned workspace users (`POST /internal/auth/setup-token`). Setup token SHA-256 hashes are stored in `auth_db.setup_tokens` until consumed via `POST /auth/credentials/setup`.

### 2. Multi-Tenant RBAC & Domain Permission Registration
Domain services register their required permissions (e.g. `orders:create`, `orders:read`) with `auth-service` upon startup via `POST /internal/permissions/register`. `auth-service` acts as an opaque central policy registry without needing domain business logic.

---

## Key Interfaces & APIs

### HTTP Endpoints (Port 8085)
| Method | Endpoint | Auth | Description |
| :--- | :--- | :--- | :--- |
| `POST` | `/auth/credentials/setup` | None | Consume single-use setup token & set user password → return JWT pair |
| `POST` | `/internal/auth/setup-token` | `X-Internal-Service-Token` | Internal endpoint for setup token generation |
| `POST` | `/internal/permissions/register` | `X-Internal-Service-Token` | Internal domain permission startup registration |
| `POST` | `/auth/login` | None | Authenticate email/password → return RS256 JWT & refresh token |
| `POST` | `/auth/refresh` | None | Rotate refresh token → issue new RS256 JWT |
| `POST` | `/auth/logout` | JWT Bearer | Revoke refresh token |
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
go test -v ./auth-service/...

# Lint check
golangci-lint run ./auth-service/...
```
