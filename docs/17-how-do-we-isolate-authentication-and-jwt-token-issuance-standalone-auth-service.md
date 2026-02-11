# How Do We Isolate Authentication and Token Issuance in a Standalone Microservice?

*Notes on Evolving Beyond X-Tenant-ID, Decoupling User Service from Auth Service, Preparing for OAuth 2.0, and Stage 1 Scaffolding Scope*

---

## 1. Evolution from `X-Tenant-ID` to Cryptographic Verification

When I built the initial prototype of this multi-tenant microservices platform, I relied on a simple `X-Tenant-ID` HTTP header passed directly to data-plane services (`order-service`, `notification-service`). This allowed me to iterate quickly and focus on solving dynamic multi-tenant connection pooling, transactional schema migrations, and isolated Docker container provisioning.

However, as the core infrastructure stabilized, relying on an unverified header presented obvious security risks:

1. **Header Forgery & Zero Proof of Authorization**: Any client could forge `X-Tenant-ID` and access data belonging to arbitrary tenants without authenticating identity or proving authorization.
2. **Missing Identity Context**: Data-plane services lacked caller context (`userID`, `email`). They knew *which* tenant database to query, but not *who* was performing the action.
3. **Inability to Scale to OAuth 2.0 / OIDC**: Continuing to pass raw tenant identifiers as an auth mechanism prevented moving toward industry-standard identity protocols.

To address these limitations, I introduced a dedicated **`auth-service`** operating under **Archetype A (API / Domain Service)** to issue and manage stateless **RS256 JWT Access Tokens** and stateful **Opaque Refresh Tokens**.

---

## 2. Decoupling `auth-service` from `user-service` for OAuth 2.0 Preparedness

When introducing authentication, I evaluated whether to place credential storage and token issuance inside `user-service` or break it out into a standalone `auth-service`. 

I chose to build `auth-service` as an independent microservice for three reasons:

### 2.1 Separation of Identity Domain vs. Security Credentials
`user-service` is responsible for user profile domain logic (`name`, `email`, profile attributes, user creation domain events). Bloating it with bcrypt password hashes, token signing keys, refresh token databases, and OAuth grant states would violate the Single Responsibility Principle and clutter domain boundaries.

### 2.2 Preparation for Future OAuth 2.0 / OpenID Connect (OIDC)
My goal for the next stage of this platform is to implement OAuth 2.0 social logins (Google, GitHub), Authorization Code grants, and Client Credentials flows. By establishing `auth-service` as a standalone service early, it can evolve into a full Authorization Server (issuing tokens, managing scopes, serving JWKS endpoints) without touching `user-service`.

### 2.3 Independent Security Boundary & Scaling
Authentication workloads (`POST /auth/login`, bcrypt hash verification, token signing) are CPU-intensive and carry different scaling and security profiles than standard user profile CRUD operations. Isolating `auth-service` allows scaling auth replicas independently and isolating database secrets (`auth_db`).

---

## 3. Architecture & Token Lifecycle Flow

```text
+-----------------------------------------------------------------------------------+
|                     Authentication & JWT Verification Topology                    |
+-----------------------------------------------------------------------------------+

[ Client App ]
      │
      ├───────────────── 1. POST /auth/login (email, password) ──────────────────┐
      │                                                                         │
      │  ┌────────────── 2. Return Access Token (RS256 JWT) ─────────────────────┤
      │  │                 + Refresh Token (Opaque Base64)                      │
      ▼  ▼                                                                      ▼
[ Traefik Gateway :8000 ]                                             [ auth-service :8085 ]
      │                                                                         │
      │ 3. HTTP Request (Authorization: Bearer <JWT>)                           ▼
      ├─────────────────────────────────────────┐                        [ authDB ]
      ▼                                         ▼                 (bcrypt & refresh_tokens)
[ order-service :8084 ]              [ notification-service :8083 ]
(RS256 Public Key Verification)      (RS256 Public Key Verification)
      │                                         │
      ▼                                         ▼
Extract tenant_id claim               Extract tenant_id & user_id
Resolve tenant database               Execute notification query
```

### 3.1 Dual-Token Strategy (Stateless Access + Stateful Refresh)

I implemented a two-token design to balance security and performance:

* **Short-Lived RS256 Access Token (Stateless, 15-Minute TTL)**: Encodes `userID`, `tenantID`, and `email` claims. Signed using a 2048-bit RSA Private Key (`AUTH_JWT_PRIVATE_KEY_PEM`). Downstream services validate tokens entirely in-memory using the corresponding RSA Public Key, incurring 0 database queries and 0 inter-service network calls.
* **Long-Lived Opaque Refresh Token (Stateful, 7-Day TTL)**: Cryptographically random 256-bit token returned once to the client. Only its SHA-256 hex digest (`token_hash`) is saved in `auth_db.refresh_tokens`.

```text
+-----------------------------------------------------------------------------------+
|                        Single-Use Refresh Token Rotation                          |
+-----------------------------------------------------------------------------------+

[ Client ] ─────► POST /auth/refresh { "refresh_token": "raw_rt_v1" }
                        │
                        ▼
                [ auth-service ]
                        │
                        ├─► 1. Compute SHA-256("raw_rt_v1") -> token_hash_1
                        ├─► 2. Query public.refresh_tokens WHERE token_hash = token_hash_1
                        ├─► 3. Validate expires_at > NOW() AND revoked_at IS NULL
                        ├─► 4. Hard DELETE token_hash_1 (Invalidate old token)
                        ├─► 5. Generate new raw_rt_v2 & insert token_hash_2
                        │
                        ▼
[ Client ] ◄───── Return { "access_token": "jwt_v2", "refresh_token": "raw_rt_v2" }
```

### 3.2 Key Verification & Zero Circular Dependencies
To prevent circular startup blockages and eliminate latency overhead on data-plane calls:
- Downstream services (`order-service`, `notification-service`) load the RSA Public Key from `AUTH_JWT_PUBLIC_KEY_PEM` at startup.
- `auth-service` exposes `GET /.well-known/jwks.json` (RFC 7517) for external gateway integration or future dynamic key discovery.

---

## 4. Current Stage 1 Limitations & Known Scope

While this Stage 1 authentication setup establishes clean microservice boundaries, I have explicitly scoped several trade-offs for local iteration and testing:

### 4.1 Production Credential Bootstrapping: Zero-Leak Setup Token Flow
To bridge open registration with authentication without broadcasting plaintext credentials over RabbitMQ, the system implements an **In-Memory Synchronous Handoff Password Setup Flow**:

1. **Password-Less Open Registration**: `POST /api/register` accepts workspace identity requests without taking passwords, ensuring credentials never touch outbox tables or message queues.
2. **Barrier Sync & Synchronous Fetch**: `notification-service` waits for both `user.created` and `workspace.ready` events. After the transaction commits, it makes a synchronous internal HTTP request `POST /internal/auth/setup-token` (authenticated via `X-Internal-Service-Token`).
3. **In-Memory Token Handoff**: `auth-service` generates a 256-bit setup token, persists `SHA-256(raw_token)` in `auth_db.password_setup_tokens`, and returns the raw string in memory. `notification-service` embeds `http://localhost:8000/setup-password?token=RAW_TOKEN` into the welcome email without writing it to disk.
4. **Token Consumption & Auto-Login**: The client calls `POST /auth/credentials/setup` `{ token, password }`. `auth-service` verifies the hash, sets the bcrypt password, invalidates the setup token, and returns RS256 JWT access and refresh tokens.

### 4.2 Known Stage 2 Limitations
1. **Public Key Distribution**: Downstream services load the RSA Public Key via environment variables rather than fetching it dynamically from JWKS over HTTP, avoiding runtime network dependencies during early development.
2. **Single Tenant Assignment per Credential**: Credentials store a primary `tenant_id` directly in `user_credentials` for token issuance, avoiding a cross-service HTTP call to `user-service` during login.

---

## 5. Next Stage Roadmap

My plan for future development stages includes:

1. **OAuth 2.0 / OIDC Integration**: Expanding `auth-service` to support Authorization Code flow, PKCE for SPA clients, and social identity providers (Google, GitHub).
2. **Token-Gated Workspace Invites**: Extending setup tokens for additional team member invites dispatched via `notification-service`.
3. **Multi-Tenant Role-Based Access Control (RBAC)**: Expanding JWT claims to encode tenant-specific roles (`admin`, `member`, `viewer`) for granular authorization in data-plane handlers.
