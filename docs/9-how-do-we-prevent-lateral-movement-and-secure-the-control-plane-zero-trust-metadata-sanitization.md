# How Do We Prevent Lateral Movement & Secure the Control Plane? Zero-Trust Metadata Sanitization

*An Engineering Deep Dive into Eliminating Password Storage in Control Plane Databases, Deleting Ghost HTTP Write-Back Endpoints, and Enforcing Inter-Service Token Authorization in Go*

---

## 1. The Vulnerability: The Lateral Movement Trap & Secret Hoarding

In microservice architectures, a central **Control Plane** (such as `tenant-service`) maintains a central directory of tenant metadata and service infrastructure connection strings.

In early implementations, the `tenant_services` table in `tenant_manager_db` stored raw Data Source Names (DSNs) as plaintext strings:

```sql
-- THE VULNERABLE SCHEMA (tenant_services table)
CREATE TABLE public.tenant_services (
    tenant_id     VARCHAR(36)  NOT NULL,
    service_name  VARCHAR(100) NOT NULL,
    dsn           TEXT         NOT NULL, --  STORES PLAINTEXT PASSWORDS (e.g. postgres://postgres:secret@host/db)
    schema_name   VARCHAR(255),
    PRIMARY KEY (tenant_id, service_name)
);
```

### Why This Creates a Catastrophic Security Flaw

1. **The Lateral Movement Trap**: If a hacker breaches a minor peripheral service (or deploys a malicious container inside the internal bridge network), they can issue requests to the unauthenticated internal HTTP endpoint (`GET /internal/tenants/:id/infrastructure/order-service`) or dump `tenant_manager_db`. This immediately exposes the root database passwords for every tenant in the enterprise.
2. **Control Plane Secret Hoarding**: In Clean Architecture, the Control Plane's sole responsibility is **Routing Metadata** (knowing *where* a tenant lives). Storing database passwords alongside host addresses forces the Control Plane to hoard domain-level secrets it does not need.
3. **Ghost HTTP Write-Back Endpoints**: Application workers sending synchronous HTTP `PATCH` requests back to the Control Plane creates unnecessary HTTP routes (`PATCH /internal/tenants/:id/infrastructure`) and introduces split-brain risks if the HTTP call fails after database creation succeeds.

---

## 2. Control Plane Schema Sanitization (Metadata-Only Registry)

To eliminate the secret hoarding vulnerability, we refactored `tenant_manager_db` to store **only non-sensitive network routing metadata**.

### 1. Refactored Schema DDL (`infrastructure/init.sql`)

```sql
-- REFACTORED SECURE SCHEMA (tenant_services table)
CREATE TABLE IF NOT EXISTS public.tenant_services (
    tenant_id     VARCHAR(36)  NOT NULL REFERENCES public.tenants(id) ON DELETE CASCADE,
    service_name  VARCHAR(100) NOT NULL,
    db_host       VARCHAR(255) NOT NULL, -- e.g. "postgres-tenant-abc" or "postgres"
    db_port       INT          NOT NULL DEFAULT 5432,
    db_name       VARCHAR(255) NOT NULL, -- e.g. "postgres" or "shared_db"
    db_user       VARCHAR(255) NOT NULL, -- e.g. "postgres"
    schema_name   VARCHAR(255),          -- e.g. "public" or "tenant_abc_order_db"
    checked_in_at TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    PRIMARY KEY (tenant_id, service_name)
);
```

### 2. Harmless Routing Responses
When an authorized domain service queries `GET /internal/tenants/:tenant_id/infrastructure/order-service`, the JSON payload contains **zero credentials**:

```json
{
  "status": "success",
  "data": {
    "db_host": "postgres-tenant-abc",
    "db_port": 5432,
    "db_name": "postgres",
    "db_user": "postgres",
    "schema_name": "public"
  }
}
```

> **Security Guarantee:** If an attacker scrapes `tenant_manager_db` or intercepts internal HTTP routing responses, all they get is a list of internal hostnames and ports. Without the master secret key, those hostnames are useless.

---

## 3. Deleting Ghost Endpoints & Pure Event Choreography

We permanently deleted the `UpdateInfrastructure` HTTP handler and `PATCH /internal/tenants/:id/infrastructure` route from `tenant-service`.

All Control Plane directory updates happen **strictly via RabbitMQ event consumption**:

```
[ infra-provisioner ] ──► Emits: infrastructure.provisioned (Routing Metadata)
                                │
                                ▼
  [ order-service ]   ──► Runs Migrations & Emits: tenant.order_db.ready (Routing Metadata)
                                │
                                ▼
 [ tenant-service ]   ──► Consumes event, updates tenant_services DB, activates Tenant
```

### Consumer Implementation (`internal/consumer/tenant_ready_consumer.go`)
```go
func (c *TenantOrderDBReadyConsumer) Start(ctx context.Context) error {
    // Consumes tenant.order_db.ready event
    // Calls HandleInfrastructureUpdate to upsert routing metadata into tenant_services
    return c.txManager.WithTransaction(ctx, func(txCtx context.Context) error {
        return c.workspaceService.HandleInfrastructureUpdate(txCtx, service.InfraUpdateInput{
            TenantID:    evt.TenantID,
            ServiceName: evt.ServiceName,
            DBHost:      evt.DBHost,
            DBPort:      evt.DBPort,
            DBName:      evt.DBName,
            DBUser:      evt.DBUser,
            SchemaName:  evt.SchemaName,
        })
    })
}
```

---

## 4. Zero-Trust Inter-Service Token Authorization (`X-Internal-Service-Token`)

Having internal HTTP endpoints is standard for decoupling services, but leaving them unauthenticated allows any compromised container on the bridge network to query routing metadata.

We implemented an **Inter-Service Authorization Middleware**:

### 1. `InternalAuthMiddleware` (`internal/middleware/auth_middleware.go`)
```go
func InternalAuthMiddleware(expectedToken string) gin.HandlerFunc {
    return func(c *gin.Context) {
        token := c.GetHeader("X-Internal-Service-Token")
        if token == "" || token != expectedToken {
            utils.WriteError(c, http.StatusForbidden, "Unauthorized inter-service access")
            c.Abort()
            return
        }
        c.Next()
    }
}
```

### 2. Protected Routing in `cmd/router.go`
```go
internal := r.Group("/internal/tenants")
internal.Use(middleware.InternalAuthMiddleware(internalToken))
{
    internal.GET("/:tenant_id/infrastructure/:service_name", workspaceHandler.GetServiceInfrastructure)
}
```

### 3. Traefik Gateway Network Isolation
Traefik gateway routes are configured to match **only** public entrypoints (`/api/register`, `/api/orders`). Requests targeting `/internal/*` from outside the cluster are rejected at the edge.

---

## 5. Declarative Configuration-Driven Bootstrapping & Domain Isolation

To avoid giving domain services superuser access while respecting the **Open/Closed Principle (OCP)**, we use **Configuration-Driven Bootstrapping**.

```
┌───────────────────────────────────────────────────────────────────────────────────────────┐
│ INFRASTRUCTURE LAYER (infra-provisioner - Isolated Worker, No Public Ports)               │
│ Injected with:                                                                            │
│   - INFRA_MASTER_SECRET (Root container password)                                         │
│   - DOMAIN_SECRETS = {"order_db": "sec_123", "inventory_db": "sec_456"}                   │
│                                                                                           │
│ 1. Spawns Container postgres-tenant-abc with POSTGRES_PASSWORD=Derive(INFRA_SECRET)       │
│ 2. Iterates over DOMAIN_SECRETS map generically:                                          │
│    - Creates 'order_db' & 'order_user' with PASSWORD = Derive("sec_123", tenantID)        │
│    - Creates 'inventory_db' & 'inventory_user' with PASSWORD = Derive("sec_456", tenantID)│
└──────────────────────────────────────────┬────────────────────────────────────────────────┘
                                           │
                                           ▼
┌────────────────────────────────────────────────────────────────────────────────────────┐
│ DATA PLANE LAYER (order-service - Application Container)                               │
│ Injected with ONLY:                                                                    │
│   - ORDER_SERVICE_SECRET ("sec_123")                                                   │
│                                                                                        │
│ 1. Connects strictly as 'order_user' using password Derived from ORDER_SERVICE_SECRET  │
│ 2. Has ZERO access to INFRA_MASTER_SECRET or INVENTORY_SERVICE_SECRET                  │
└────────────────────────────────────────────────────────────────────────────────────────┘
```

### Generic Bootstrapping Loop (`infra-provisioner/internal/docker/provisioner.go`)
```go
for domainDB, domainSecret := range domainSecrets {
    user := domainDB + "_user"
    pass := crypto.DeriveTenantDBPassword(domainSecret, tenantID)
    
    db.Exec(fmt.Sprintf("CREATE DATABASE %s;", domainDB))
    db.Exec(fmt.Sprintf("CREATE USER %s WITH PASSWORD '%s';", user, pass))
    db.Exec(fmt.Sprintf("GRANT ALL PRIVILEGES ON DATABASE %s TO %s;", domainDB, user))
}
```

> **Security & Clean Architecture Win:**
> - `infra-provisioner` Go code is 100% generic (adding a new `billing-service` requires updating configuration map, 0 code edits in Go).
> - `order-service` possesses ONLY `ORDER_SERVICE_SECRET` and connects as `order_user`.
> - If `order-service` suffers an RCE, PostgreSQL role-level permissions physically block `order_user` from querying `inventory_db` or `billing_db`.

---

## 6. Defense in Depth: The Multi-Stage Exploit Chain & Secret Rotation

This architecture shifts the threat model from a **Single Point of Failure (Honeypot Model)** to a **High-Friction Exploit Chain (Defense-in-Depth Model)**.

```
[ Stage 1: Target Discovery ]          [ Stage 2: Secret Extraction ]        [ Stage 3: Cryptographic Replay ]
Find Target TenantID & DBHost          Breach Container Memory/Env           Reconstruct HMAC-SHA256 Derivation
(e.g., "tnt_99" on port 5432)   +      to steal ORDER_SERVICE_SECRET  +      Formula & Domain Prefix ("tenant_db_v1_")
                                       (Only in order-service container)
```

### Exploit Chain Analysis
1. **Stage 1 (Target Discovery)**: Obtain the `tenant_id` and routing metadata (`db_host`, `db_port`). This only reveals *where* the target database is hosted, providing zero connection credentials.
2. **Stage 2 (Secret Extraction)**: Breach the environment variables or process memory of `order-service` to extract `ORDER_SERVICE_SECRET`. Compromising non-database services (`user-service`, `notification-service`, `web-ui`) yields nothing because those containers do not possess `ORDER_SERVICE_SECRET`.
3. **Stage 3 (Cryptographic Derivation)**: Reverse-engineer the derivation algorithm (`HMAC-SHA256` + domain prefix string `"tenant_db_v1_"` + `pg_` prefix truncation).

### Versioned Secret Rotation
Because the derivation string embeds a version identifier (`"tenant_db_v1_"`):

```go
h.Write([]byte("tenant_db_v1_" + tenantID))
```

If `ORDER_SERVICE_SECRET` is compromised or scheduled for routine compliance rotation, security engineers can update the prefix to `"tenant_db_v2_"` alongside a new secret key. `infra-provisioner` can re-key containers without requiring architectural changes to the domain data plane.

---

## 7. Architecture Security Matrix

| Security Threat | Legacy Implementation | Zero-Trust Refactored Implementation | Security Gain |
| :--- | :--- | :--- | :--- |
| **Control Plane DB Scraping** | Plaintext DSN strings with passwords | Sanitized host/port/name metadata only | Zero passwords stored in central DB |
| **Lateral Movement Attack** | Unauthenticated internal HTTP endpoints | `X-Internal-Service-Token` Middleware | Rejects unauthorized inter-container calls |
| **External API Interception** | Ghost `PATCH /internal/*` endpoints | Ghost routes deleted; Traefik blocks `/internal/*` | Inaccessible from public internet |
| **Network Secret Leakage** | Credentials sent across HTTP/RabbitMQ | Stateless `HMAC-SHA256` memory derivation | Zero secret transmission across network |
| **Root Key Leakage to Apps** | Root secret given to domain app | `INFRA_MASTER_SECRET` kept *only* in `infra` | Data plane app has zero root superuser keys |
| **Inter-Domain Secret Leakage**| Shared global secret | Per-domain secret (`ORDER_SERVICE_SECRET`) | Compromising domain A leaks no domain B keys |
