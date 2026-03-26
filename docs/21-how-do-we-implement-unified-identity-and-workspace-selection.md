# Unified Identity and Workspace Selection in Multi-Tenant Microservices

*Structuring global credentials, membership associations, single-use exchange tokens, and tenant-bound refresh tokens.*

---

## 1. Moving from Per-Tenant Credentials to Unified Identity

In early prototypes, credentials were often modeled as one row per `(email, tenant_id)`. Joining a second workspace required creating another credential row and setting a separate password.

This creates obvious user experience and data modeling problems:
- A user with one email address has multiple unrelated user accounts.
- Password updates in one workspace do not reflect in others.
- No unified dashboard exists to list all workspaces associated with an email.

We adopt the **Unified Identity** model (similar to Slack and GitHub):

> A user has **one global identity and one password** across the platform. The `user_credentials` table stores **one row per email**. A separate table, `user_tenant_memberships (user_id, tenant_id)`, records which workspaces that identity belongs to.

---

## 2. Schema Refactoring: Decoupling Credentials from Tenants

```text
user_db.public.users                  auth_db.public.user_credentials        auth_db.public.user_tenant_memberships
┌──────────────────────────┐          ┌───────────────────────────────────┐  ┌─────────────────────────────────────────┐
│ id          PK           │          │ user_id         PK                │  │ user_id    FK ────────────────────┐     │
│ email       UNIQUE       │          │ email           UNIQUE (1/email)  │  │ tenant_id                         │     │
│ name                     │          │ password_hash   (bcrypt)          │  │ created_at                        │     │
│ created_at / updated_at  │          │ created_at / updated_at           │  │ PRIMARY KEY (user_id, tenant_id)  │     │
└──────────────────────────┘          └───────────────────────────────────┘  └─────────────────────────────────────────┘
                                          │  No tenant_id column!              │
                                          └──────────────┬─────────────────────┘
                                                         ▼
                                         Tenant context resolved at token issuance
                                         via GetUserMemberships()
```

### Removing Sentinel Values
An earlier iteration used a placeholder sentinel (`SELECT '' AS tenant_id`) so existing credential scanners would compile without modifications. However, this introduced a subtle bug: if any token issuance code path failed to explicitly overwrite the sentinel before minting, it produced a tenant-less access token.

The design eliminates sentinels at the type signature level:

```go
func (s *AuthService) IssueTokenPair(ctx context.Context, userID, tenantID, email string) (*TokenPair, error)
```

`tenantID` is a mandatory parameter. Callers must supply an explicit, verified workspace identifier:
- **Single-Tenant User Login**: Automatically resolves `GetUserMemberships()[0]`.
- **Multi-Tenant User Login**: Returns an intermediate exchange token, requiring the client to select a workspace.
- **Refresh Flow**: Preserves the `tenant_id` stored on the refresh token row at original issuance.

---

## 3. The Login and Selection Workflow

```text
Client ──► POST /api/v1/auth/login (email, password)
                │
                ▼
          [ auth-service ] (Verifies bcrypt hash)
                │
                ├─► User belongs to 1 tenant:
                │     └─► Mints Access Token (bound to tenant) + Refresh Token
                │
                └─► User belongs to multiple tenants:
                      └─► Returns exchange_token + list of available workspaces
                                │
Client ──► POST /api/v1/auth/select-workspace (exchange_token, selected_tenant_id)
                │
                ▼
          [ auth-service ]
                └─► Validates exchange token & membership
                └─► Mints Access Token (bound to selected tenant) + Refresh Token
```

---

## 4. Architectural Invariants & Operational Trade-offs

- **Single Global Credential**: A user maintains one email and password record across the platform; workspace access is governed entirely via membership join tables.
- **Elimination of Sentinel Values**: Type signatures strictly mandate explicit `tenantID` arguments; empty string sentinels are prohibited across auth and token services.
- **Two-Step Multi-Tenant Login**: Users with multiple memberships receive a short-lived, single-use exchange token to choose their target workspace, preventing accidental default tenant routing.
