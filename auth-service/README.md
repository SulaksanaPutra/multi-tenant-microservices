# auth-service

**Archetype A**: API / Domain Service

## Responsibility
Issues and validates RS256 JWT access tokens and opaque refresh tokens.
Stores bcrypt-hashed credentials in `auth_db`.

## Endpoints

| Method | Path | Auth | Description |
|---|---|---|---|
| `POST` | `/auth/credentials/setup` | None | Single-use setup token + password → set password & return JWT pair |
| `POST` | `/internal/auth/setup-token` | `X-Internal-Service-Token` | Internal endpoint to generate password setup token |
| `POST` | `/auth/login` | None | Email + password → JWT + refresh token |
| `POST` | `/auth/refresh` | None | Rotate refresh token → new JWT |
| `POST` | `/auth/logout` | JWT Bearer | Revoke refresh token |
| `GET` | `/.well-known/jwks.json` | None | RSA public key for downstream JWT verification |
| `GET` | `/health` | None | Health check |

## Running Locally
```bash
cp .env.example .env
# Populate AUTH_JWT_PRIVATE_KEY_PEM with an RSA-2048 key
docker compose up -d --build
```

## Generate RSA Key Pair
```bash
openssl genrsa -out private.pem 2048
openssl rsa -in private.pem -pubout -out public.pem
# For .env (single line):
awk 'NF {sub(/\r/, ""); printf "%s\\n",$0;}' private.pem
```
