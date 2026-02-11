# Authentication Architecture: Standalone Auth-Service and Stateless RS256 Tokens

*Decoupling identity from user profiles, asymmetric JWT verification, and preparing for OAuth 2.0 / OIDC.*

---

## 1. Evolution from `X-Tenant-ID` to Cryptographic Verification

In early development iterations, services relied on an `X-Tenant-ID` HTTP header passed by the caller. While helpful for early prototyping, this approach has obvious production limitations:

1. **Zero Proof of Authenticity**: Any caller could spoof `X-Tenant-ID` to access another tenant's data.
2. **Missing Identity Context**: Data-plane services had no cryptographic proof of *who* made the call (`user_id`, `email`, role permissions).
3. **Security Inversion**: Leaving identity verification to individual peripheral services invites configuration errors.

To address this, we extracted authentication into a dedicated **`auth-service`**, migrating to stateless **RS256 JWT Access Tokens** paired with stateful **Opaque Refresh Tokens**.

---

## 2. Decoupling `auth-service` from `user-service`

We deliberately kept `auth-service` separate from `user-service`:

- **Identity Domain vs Security Credentials**: `user-service` owns profile entities (names, emails, user avatars, profile updates). `auth-service` owns cryptographic credentials (bcrypt password hashes, RSA signing keys, refresh tokens, rate limiting).
- **OAuth 2.0 / OIDC Preparedness**: Establishing `auth-service` as an independent service allows it to evolve into an OAuth 2.0 Authorization Server (supporting Google/GitHub social logins and Client Credentials) without complicating user profile logic.
- **Independent Scaling & Hardening**: Password hashing (`bcrypt`) and token generation are CPU-intensive operations with different resource footprints and attack surfaces compared to domain profile lookups.

---

## 3. Asymmetric Verification Topology (RS256)

```text
[ Client App ]
      │
      ├─► 1. POST /api/v1/auth/login (email, password) ──► [ auth-service ] (Holds RSA Private Key)
      │                                                           │
      │◄── 2. Returns Access Token (RS256 JWT) + Refresh Token ───┘
      │
      ▼
[ Gateway / Services ] (Hold RSA Public Key Only)
      │
      ├─► GET /api/v1/orders (Bearer JWT) ──► [ order-service ]
      │                                            └─► In-memory signature check via Public Key
      │                                                (Zero network calls, 0ms latency overhead)
      ▼
```

### Dual-Token Lifecycle:
- **Access Token (RS256 JWT, 15-Minute TTL)**: Contains caller claims (`user_id`, `tenant_id`, `permissions`, `perm_version`). Downstream services verify signatures locally in memory using the RSA public key with zero database or inter-service network lookups.
- **Refresh Token (Opaque String, 7-Day TTL)**: Cryptographically random 256-bit string. Only its SHA-256 hash is persisted in `auth_db`. Used to obtain new access tokens without re-entering credentials.

---

## 4. Architectural Invariants & Operational Trade-offs

- **Asymmetric Signature Verification**: Edge and downstream services verify JWT signatures locally using the RSA public key, eliminating network hops on request hot paths.
- **Identity Domain Separation**: `auth-service` manages credentials, crypto keys, and token lifecycles; `user-service` strictly owns domain profile entities and attributes.
- **Stateless vs Stateful Dual-Token Model**: Short-lived RS256 access tokens (15m TTL) provide high throughput, while hashed opaque refresh tokens (7d TTL) in PostgreSQL maintain revocation control.
