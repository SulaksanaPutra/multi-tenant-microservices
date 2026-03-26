# How Do We Implement Unified Identity and Workspace Selection?

*Replacing Fragmented Per-Tenant Credentials with a Single Global Identity, Single-Use Exchange Tokens, and Tenant-Bound Refresh Tokens*

---

## 1. The Problem I Started With: One Credential Per (Email, Tenant)

When I first modeled authentication, `user_credentials` was keyed by **1 row per (email, tenant)**: every workspace a user joined produced a brand-new credential row with its own bcrypt hash and its own `tenant_id`. At the time it felt simple and self-contained — each tenant was an island, and each island had its own password.

That illusion lasted exactly until a real user belonged to two workspaces. Three concrete problems surfaced almost immediately:

1. **Fragmented identity** — the same user (e.g. `john@example.com`) was modeled as N unrelated auth records, one per workspace.
2. **Duplicate password provisioning** — registering a 2nd workspace demanded a fresh setup-token + password-setup round trip, and there was no notion of "I already know this person".
3. **No workspace selection** — login was bound to exactly one `tenant_id`, so a user belonging to two workspaces had no first-class way to pick which context to enter.

I decided to adopt the **Unified Identity + Workspace Selection** paradigm used by Slack, GitHub, and Vercel:

> A user has **one global identity and one master password** across the platform. `user_credentials` stores **1 row per email**. A join table `user_tenant_memberships (user_id, tenant_id)` records *which workspaces* that identity belongs to.

When `john@example.com` registers a 2nd workspace, `user-service` and `auth-service` detect the existing identity and simply attach a new `user_tenant_memberships` row instead of creating duplicate credentials.

---

## 2. The Refactor: Dropping `tenant_id` from `user_credentials`

```text
 user_db.public.users                  auth_db.public.user_credentials        auth_db.public.user_tenant_memberships
 ┌──────────────────────────┐          ┌───────────────────────────────────┐  ┌─────────────────────────────────────────┐
 │ id          PK           │          │ user_id         PK                │  │ user_id    FK ────────────────────┐     │
 │ email       UNIQUE       │          │ email           UNIQUE (1/email)  │  │ tenant_id                         │     │
 │ name                     │          │ password_hash   (bcrypt)          │  │ created_at                        │     │
 │ created_at / updated_at  │          │ created_at / updated_at           │  │ PRIMARY KEY (user_id, tenant_id)  │     │
 └──────────────────────────┘          └───────────────────────────────────┘  └─────────────────────────────────────────┘
                                           │  NO tenant_id column!              │
                                           └──────────────┬─────────────────────┘
                                                         ▼
                                         tenant context resolved at ISSUANCE time
                                         via GetUserMemberships()
```

**Key decision: `user_credentials` dropped its `tenant_id` column.**

- The credential row is now a **global identity row**: it answers only *"who is this user and what is their password hash?"*.
- Tenant context lives in `user_tenant_memberships`. The credential repository reads it separately (`GetUserMemberships`).
- Because the column no longer exists, `FindByEmail`/`FindByUserID` select **5 columns** and `Credential` no longer carries a `TenantID` field at all.

### The Footgun I Shipped First: `'' AS tenant_id`

An intermediate version of the refactor kept a **sentinel** (`SELECT '' AS tenant_id`) so `scanCredential` could keep scanning into `domain.Credential.TenantID`, and each issuance path would overwrite it with the real tenant before minting a token. That worked, but it was a **footgun**: any code path that issued a token from a raw credential *without* overwriting the sentinel silently minted a **tenant-less JWT**. That is exactly what happened in the refresh flow — I found it during testing, and it convinced me the sentinel had to go.

The final design removes the footgun at the type level:

```go
func (s *AuthService) issuePair(ctx context.Context, userID, tenantID, email string) (*TokenPair, error)
```

`issuePair` now takes the tenant as an **explicit parameter** — it is impossible to forget to resolve it. Every caller must obtain a concrete `tenantID`:
- **Login (1 membership):** `GetUserMemberships()[0]`
- **SelectWorkspace:** the `tenant_id` chosen by the client
- **RefreshToken:** the `tenant_id` stored on the refresh-token row at issuance

---

## 3. How Memberships Get Created

Memberships are written in two places, both idempotent (`ON CONFLICT (user_id, tenant_id) DO NOTHING`):

1. **`InternalAuthService.CreatePasswordSetupToken`** (`auth-service`) — when a setup token is minted for a workspace, it attaches the membership immediately.
2. **`CredentialRepository.UpsertCredential`** (`auth-service`) — when the password is actually persisted, the membership is attached again (defensive no-op).

On the profile side, **`user-service` reuses the existing user row** when the owner email is already known (`CreateUserFromWorkspace` → `GetUserByEmail` → reuse `userID`) instead of creating a duplicate profile.

```text
[ Register Workspace #2 (same email) ]
        │
        ▼
[ tenant-service ]  workspace.initiated ──► [ user-service ]
                                                 │ GetUserByEmail("john@example.com")  -> exists
                                                 │ reuse existing user_id (NO duplicate row)
                                                 ▼
                                            [ auth-service ] internal setup-token
                                                 │ AddMembership(user_id, tenant_B)   (idempotent)
                                                 │ setup-token bound to tenant_B
                                                 ▼
                                            [ Client ] POST /api/auth/credentials/setup
                                                 │ UpsertCredential -> upserts THE SAME credential row
                                                 │   + AddMembership(user_id, tenant_B) (no-op)
```

After this, `user_credentials` still has **one row** for `john@example.com`, while `user_tenant_memberships` holds `[tenant_A, tenant_B]`.

---

## 4. Login: One Workspace vs. Several

```text
[ Client ] ────► POST /api/auth/login { "email", "password" }
                      │
                      ▼
              [ auth-service ]
                      │ 1. FindByEmail()      -> 1 global credential row
                      │ 2. bcrypt.CompareHashAndPassword()  -- fail? -> 401 Invalid Credentials
                      │ 3. GetUserMemberships(user_id)
                      │      SELECT tenant_id FROM user_tenant_memberships WHERE user_id = $1
                      │
                      ├──────────────┬──────────────────────────────
                      ▼              ▼
              0 memberships   1+ memberships
                      │              │
                      ▼              ▼
                401 No Tenant   create single-use EXCHANGE TOKEN
                  Membership     (10 min TTL, bound to user identity)
                                 │
▼
                  { status: "SELECT_WORKSPACE",
                    exchange_token,
                    workspaces: [{ "tenant_id": "tntA" },
                                 { "tenant_id": "tntB" }] }
```

**Login response** — every successful login returns the exchange token, **not** a JWT. The client exchanges it in a follow-up call (silently when only one workspace exists, or via a workspace-selection modal when there are several):

```json
{
  "status": "success",
  "message": "Multiple workspace accounts found. Please select a workspace.",
  "data": {
    "status": "SELECT_WORKSPACE",
    "exchange_token": "<single-use, 10-min TTL>",
    "workspaces": [
      { "tenant_id": "tntA" },
      { "tenant_id": "tntB" }
    ]
  }
}
```

**Zero-membership** is a hard error (`ErrNoTenantMembership`): a credential without any membership cannot produce a scoped token, and I refused to mint a tenant-less one.

---

## 5. Workspace Selection: The Single-Use Exchange Token (`POST /api/auth/select-tenant`)

The exchange token is deliberately **not a JWT and not a refresh token** — it is a single-use opaque token stored (hashed) in `password_setup_tokens`, the same table as password setup tokens. Reusing that table was intentional: it already gives us hashed-at-rest storage, single-use semantics (`used_at`), and an expiry column — I didn't need to invent a fourth token type.

```text
[ Client ] ──► POST /api/auth/select-tenant { "exchange_token", "tenant_id" }
                      │
                      ▼
              [ auth-service ]
                      │ 1. Hash exchange token & look up in password_setup_tokens
                      │ 2. Reject if: not found / already used (used_at set) / expired (10 min TTL)
                      │ 3. MarkTokenUsed()  (single-use)
                      │ 4. issuePair(user_id, tenant_id, email)   <- tenant chosen by client
                      │
                      ▼
        { access_token: <JWT scoped to tenant_id>, refresh_token: <raw>, expires_in: 900 }
```

Security properties:
- **Single-use** — a stolen exchange token cannot be replayed (`MarkTokenUsed`).
- **Short TTL** — 10 minutes, so it is only usable during an active login attempt.
- **Tenant scoping** — the resulting JWT is bound to the chosen `tenant_id`; permissions and `perm_version` are fetched for exactly that tenant.
- **Client cannot fabricate a workspace** — the exchange token is server-issued; `SelectWorkspace` re-verifies that the requested `tenant_id` is one of the user's memberships (`GetUserMemberships`) before issuing, so the client may only enter an already-attached workspace.

---

## 6. The Regression I Nearly Missed: Tenant-Bound Refresh Tokens

A subtle regression surfaced during the refactor — exactly the sentinel footgun I described in §2, biting for real. `refresh_tokens` originally stored only `user_id`, so after a refresh the service looked up the credential — which no longer has a tenant — and minted a **tenant-less JWT**. Workspace context silently vanished on token rotation.

Fix: `refresh_tokens` gained a `tenant_id` column, populated at issuance time:

```sql
CREATE TABLE public.refresh_tokens (
    id         UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id    TEXT        NOT NULL,
    tenant_id  TEXT        NOT NULL DEFAULT '',
    token_hash TEXT        UNIQUE NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL,
    revoked_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
```

`RefreshToken` restores the workspace from the stored row before calling `issuePair`:

```go
rt, err := s.tokenRepository.FindByTokenHash(ctx, tokenHash)
// ...validate revoked/expired...
cred, err := s.credentialRepository.FindByUserID(ctx, rt.UserID)
// ...
return s.issuePair(ctx, rt.UserID, rt.TenantID, cred.Email) // <-- tenant survives rotation
```

Because the refresh token is bound to a specific tenant at login/selection time, rotation stays inside the same workspace.

---

## 7. The API Contract

| Method | Endpoint | Auth | Behavior |
| :--- | :--- | :--- | :--- |
| `POST` | `/api/auth/login` | None | 1+ memberships → `SELECT_WORKSPACE` + single-use exchange token + workspace list; 0 → 401 |
| `POST` | `/api/auth/select-tenant` | None | Exchange token + `tenant_id` → JWT pair scoped to that tenant |
| `POST` | `/api/auth/refresh` | None | Rotate refresh token; tenant context preserved from stored row |
| `POST` | `/api/auth/credentials/setup` | None | Consume setup token; upsert the single global credential row |

---

## 8. What I Tested

- **Unit:** `auth-service` covers login branching, `select-tenant` (valid token, expired, already-used, non-member tenant), setup-password, refresh tenant preservation, and the strict no-membership error.
- **E2E:** `TC-E2E-024` (`tc_e2e_024_same_email_multi_tenant_registration_e2e_test.go`) registers two tenants with the **same email**, provisions credentials, and asserts that workspace selection yields two isolated JWTs with the correct `tenant_id` claims. The shared helper `loginAndGetTokenWithTenant` transparently drives the `select-tenant` flow.

---

## 9. Known Limitations & What I'd Do Next

- The `workspaces` list in the `SELECT_WORKSPACE` response carries only `tenant_id` per workspace, and **not** the user's `role` per workspace. Roles live in `user_roles` and are resolved into the JWT only at `select-tenant`/issuance time, not surfaced at login. The client resolves a user-readable label from the browser's locally-saved tenant registry when available. If a user's role were needed before selection, one would either denormalize it into the membership or expose it via an internal tenant lookup.
- The original design sketch mentioned a `role_id` column on `user_tenant_memberships`; the implemented schema stores `(user_id, tenant_id)` only, with roles modeled separately in `user_roles`.
