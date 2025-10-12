# Preventing Lateral Movement: Zero-Trust Metadata Sanitization

*Eliminating sensitive credentials from control plane registries, eliminating ghost write-back endpoints, and inter-service authentication.*

---

## 1. The Vulnerability: Secret Hoarding in Control Planes

In multi-tenant systems, a central control plane (such as `tenant-service`) maintains a directory of tenant infrastructure mappings.

In early designs, connection strings were stored as raw Data Source Names (DSNs) in the database:

```sql
-- Insecure schema: storing plaintext passwords in tenant routing catalog
CREATE TABLE public.tenant_services (
    tenant_id     VARCHAR(36)  NOT NULL,
    service_name  VARCHAR(100) NOT NULL,
    dsn           TEXT         NOT NULL, -- Insecure: stores postgres://user:password@host/db
    schema_name   VARCHAR(255),
    PRIMARY KEY (tenant_id, service_name)
);
```

### Failure Modes:
1. **Lateral Movement Trap**: If an attacker compromises a peripheral service or gains internal network read access, dumping the `tenant_services` table grants root credentials to every tenant database across the fleet.
2. **Violates Least Privilege**: The control plane only needs to know *where* tenant data lives (host, port, schema) to route traffic. Storing passwords hoards secrets it does not need.
3. **Ghost HTTP Write-Back Endpoints**: Having workers report back credentials over internal HTTP endpoints (`PATCH /internal/tenants/:id/infrastructure`) creates unneeded network surfaces and split-brain risks if the HTTP call fails after database provisioning.

---

## 2. Sanitized Metadata Registry

To resolve this, we refactor the table to store **only non-sensitive routing metadata**:

```sql
CREATE TABLE IF NOT EXISTS public.tenant_services (
    tenant_id     VARCHAR(36)  NOT NULL REFERENCES public.tenants(id) ON DELETE CASCADE,
    service_name  VARCHAR(100) NOT NULL,
    db_host       VARCHAR(255) NOT NULL,
    db_port       INT          NOT NULL DEFAULT 5432,
    db_name       VARCHAR(255) NOT NULL,
    db_user       VARCHAR(255) NOT NULL,
    schema_name   VARCHAR(255),
    checked_in_at TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    PRIMARY KEY (tenant_id, service_name)
);
```

### Sanitized Service Response
When an authorized internal service requests routing metadata via `GET /internal/tenants/:id/infrastructure/order-service`, the payload contains host details without secrets:

```json
{
  "status": "success",
  "data": {
    "db_host": "postgres-tenant-acme",
    "db_port": 5432,
    "db_name": "postgres",
    "db_user": "postgres",
    "schema_name": "public"
  }
}
```

Even if this table is inspected or intercepted, it exposes only network topology, not access credentials.

---

## 3. Stateless Credential Derivation

Instead of storing passwords in the database or passing them over message queues, services derive passwords locally using an HMAC calculation:

```go
func DeriveTenantPassword(masterSecret, tenantID string) string {
    h := hmac.New(sha256.New, []byte(masterSecret))
    h.Write([]byte(tenantID))
    return hex.EncodeToString(h.Sum(nil))[:24]
}
```

- When `infra-provisioner` provisions a dedicated container, it sets the database password using `DeriveTenantPassword(secret, tenantID)`.
- When `order-service` connects to that container, it calculates the exact same password using its own local environment variable `INFRA_MASTER_SECRET`.
- No passwords are ever stored in PostgreSQL or transmitted across AMQP message buses.

---

## 4. Architectural Invariants & Operational Trade-offs

- **Zero Credential Hoarding**: Control plane registries store network endpoints only, never plaintext or encrypted passwords.
- **Stateless Derivation**: Passwords are computed dynamically via HMAC-SHA256, eliminating inter-service secret distribution over message buses.
- **Blast Radius Containment**: A compromise of the control plane catalog exposes host addresses, preventing immediate lateral database takeovers.
