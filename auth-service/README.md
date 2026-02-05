# auth-service

**Archetype A**: API / Domain Service

## Responsibility
Issues and validates RS256 JWT access tokens and opaque refresh tokens.
Stores bcrypt-hashed credentials in `auth_db`.

## Endpoints

| Method | Path | Auth | Description |
|---|---|---|---|
| `POST` | `/auth/credentials/set` | None ⚠️ | TEMPORARY — set password for a registered user |
| `POST` | `/auth/login` | None | Email + password → JWT + refresh token |
| `POST` | `/auth/refresh` | None | Rotate refresh token → new JWT |
| `POST` | `/auth/logout` | JWT Bearer | Revoke refresh token |
| `GET` | `/.well-known/jwks.json` | None | RSA public key for downstream JWT verification |
| `GET` | `/health` | None | Health check |

## ⚠️ Temporary Scaffolding
`POST /auth/credentials/set` is non-production and will be replaced by
an email-invite / token-gated reset flow in the OAuth 2.0 stage.

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
