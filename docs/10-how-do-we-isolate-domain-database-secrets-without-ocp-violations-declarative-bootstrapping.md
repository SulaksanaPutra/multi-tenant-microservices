# How Do We Isolate Domain Database Secrets Without OCP Violations? Declarative Bootstrapping

*An Engineering Deep Dive into Solving the Root Key Distribution Trap, Enforcing PostgreSQL Role-Level Least Privilege, and Configuration-Driven Container Provisioning in Go*

---

## 1. The Big Problem: Security vs. Efficiency

When scaling a B2B SaaS platform with dedicated tenant database containers (`postgres-tenant-<id>`), supporting multiple microservices (`order-service`, `inventory-service`, `billing-service`) presents a fundamental architectural dilemma:

```
                          [ THE ARCHITECTURAL DILEMMA ]

     [ Option B: Container Explosion ]               [ Option A: The God Service ]
     Dedicated Postgres per Domain per Tenant        Infra-Provisioner Holds All Domain Secrets
     └─► 50 Tenants x 5 Domains = 250 Containers     └─► Violates Open/Closed Principle (OCP)
     └─► Bankrupts Memory / CPU Budget               └─► Must modify infra code for new services
```

### The Flawed Proposal & The Root Key Distribution Trap

To solve the OCP violation, one might propose **Autonomous Domain Bootstrapping**, where `order-service` connects to the dedicated container as the `postgres` superuser, creates its own database (`order_db`), and provisions its own role (`order_user`).

**Why This Proposal Fails (The Root Key Distribution Trap):**
If `order-service` performs initial root DDL setup, `order-service` must be injected with `INFRA_MASTER_SECRET` (the root container password).

If an attacker achieves Remote Code Execution (RCE) inside `order-service`:
1. They extract `INFRA_MASTER_SECRET` from environment variables or process memory.
2. They log into PostgreSQL as the `postgres` superuser.
3. **PostgreSQL role-level security is completely bypassed.** The attacker can read `inventory_db`, `billing_db`, or drop all databases inside the container.

> **Core Security Principle:** Data-plane application containers must **NEVER** hold infrastructure root superuser credentials.

---

## 2. The Solution: Declarative Bootstrapping

To maintain **100% Open/Closed Principle (OCP)** compliance without distributing root secrets to application containers, we implement **Declarative Configuration-Driven Bootstrapping**.

We hand the heavy lifting to `infra-provisioner` (an isolated worker service with zero public ports):

```
┌────────────────────────────────────────────────────────────────────────────────────────┐
│ INFRASTRUCTURE LAYER (infra-provisioner - Isolated Worker, No Public Ports)            │
│ Injected with:                                                                         │
│   - INFRA_MASTER_SECRET (Root container password)                                      │
│   - DOMAIN_SECRETS = {"order_db": "sec_123", "inventory_db": "sec_456"}                │
│                                                                                        │
│ 1. Spawns Container postgres-tenant-abc with POSTGRES_PASSWORD=Derive(INFRA_SECRET)    │
│ 2. Iterates over DOMAIN_SECRETS map generically:                                       │
│    - Creates 'order_db' & 'order_user' with PASSWORD = Derive("sec_123", tenantID)     │
│    - Creates 'inventory_db' & 'inventory_user' with PASSWORD = Derive("sec_456", tenantID)│
└──────────────────────────┬─────────────────────────────────────────────────────────────┘
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

- **Step 1:** The `infra-provisioner` receives `INFRA_MASTER_SECRET` (root password) and a JSON map of all domain DB secrets, e.g., `DOMAIN_SECRETS={"order_db": "sec_123", "inventory_db": "sec_456"}`.
- **Step 2:** It logs into `postgres-tenant-<id>` as the root `postgres` superuser and iterates generically over the JSON map.
- **Step 3:** For each entry, it creates the database (`order_db`), creates a restricted role (`order_user`), and sets a password derived mathematically via HMAC-SHA256.
- **Step 4:** `order-service` is injected with **only** `ORDER_SERVICE_SECRET` ("sec_123") and connects strictly as the restricted `order_user`, completely isolated from other domain databases.

---

## 3. Cryptographic Secret Derivation & Role Isolation

### 1. Deterministic Password Derivation
Both `infra-provisioner` and `order-service` calculate the final database password independently using HMAC-SHA256:

$$\text{Password} = \text{HMAC-SHA256}(\text{DomainSecret}, \text{"tenant\_db\_v1\_"} + \text{TenantID})$$

```go
func DeriveTenantDBPassword(secret, tenantID string) string {
    h := hmac.New(sha256.New, []byte(secret))
    h.Write([]byte("tenant_db_v1_" + tenantID))
    return fmt.Sprintf("pg_%s", hex.EncodeToString(h.Sum(nil))[:24])
}
```

### 2. Locking Down Role Permissions
Inside `postgres-tenant-<id>`, PostgreSQL acts as a strict role-based guard:
- `order_user` is granted permissions **only** on `order_db`.
- If a hacker takes over `order-service`, they possess only `ORDER_SERVICE_SECRET` and connect as `order_user`.
- If the attacker attempts to query inventory data:

```sql
-- Executed by attacker logged in as order_user:
SELECT * FROM inventory_db.public.products;
-- ERROR: permission denied for database inventory_db
```

---

## 4. The Clean Architecture Code

The Go code inside `infra-provisioner` contains **zero hardcoded domain names**, fully satisfying the Open/Closed Principle (OCP):

```go
// Generic Bootstrapping Loop (infra-provisioner/internal/docker/provisioner.go)
func (p *DockerProvisioner) bootstrapDomainDatabases(
    ctx context.Context,
    host string, port int,
    rootUser, rootPassword, rootDBName string,
    tenantID string,
    domainSecrets map[string]string,
) error {
    dsn := fmt.Sprintf("host=%s port=%d user=%s password=%s dbname=%s sslmode=disable",
        host, port, rootUser, rootPassword, rootDBName)

    db, err := sql.Open("postgres", dsn)
    if err != nil {
        return err
    }
    defer db.Close()

    for domainDB, domainSecret := range domainSecrets {
        if err := ValidateIdentifier(domainDB); err != nil {
            return fmt.Errorf("domainDB identifier validation failed for '%s': %w", domainDB, err)
        }

        user := fmt.Sprintf("%s_user", strings.TrimSuffix(domainDB, "_db"))
        if err := ValidateIdentifier(user); err != nil {
            return fmt.Errorf("user identifier validation failed for '%s': %w", user, err)
        }

        pass := crypto.DeriveTenantDBPassword(domainSecret, tenantID)
        quotedDB := pq.QuoteIdentifier(domainDB)
        quotedUser := pq.QuoteIdentifier(user)

        // Create Database if missing
        var dbExists bool
        _ = db.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM pg_database WHERE datname = $1);", domainDB).Scan(&dbExists)
        if !dbExists {
            if _, err := db.ExecContext(ctx, fmt.Sprintf("CREATE DATABASE %s;", quotedDB)); err != nil {
				return err
			}
        }

        // Create or Update Role
        var roleExists bool
        _ = db.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM pg_roles WHERE rolname = $1);", user).Scan(&roleExists)
        if !roleExists {
            if _, err := db.ExecContext(ctx, fmt.Sprintf("CREATE USER %s WITH PASSWORD %s;", quotedUser, pq.QuoteLiteral(pass))); err != nil {
				return err
			}
        }

        // Grant privileges
        db.ExecContext(ctx, fmt.Sprintf("GRANT ALL PRIVILEGES ON DATABASE %s TO %s;", quotedDB, quotedUser))
    }

    return nil
}
```

---

## 5. The "Poison Pill" Event: Defense Against Asynchronous DDL SQL Injection

In event-driven architectures, an attacker might breach a peripheral service and publish a fake, malicious RabbitMQ message with a payload like:

```json
{
  "tenant_id": "tnt_abc",
  "domainDB": "order_db; DROP DATABASE postgres; --"
}
```

### Why DDL Queries Require Special Protection in PostgreSQL
In PostgreSQL's wire protocol, standard queries (`SELECT`, `INSERT`) support `$1` parameterized placeholders. However, **Data Definition Language (DDL)** queries (`CREATE DATABASE`, `CREATE USER`) **refuse `$1` placeholders**.

Because DDL requires dynamic string formatting (`fmt.Sprintf`), un-sanitized payload inputs create a dangerous **Asynchronous SQL Injection (Async SQLi)** vector.

### The Dual-Layer Defense Solution

```
┌────────────────────────────────────────────────────────────────────────────────────────┐
│ DUAL-LAYER ASYNC SQL INJECTION DEFENSE (infra-provisioner)                             │
│                                                                                        │
│ Layer 1: Strict Whitelist Regex Validation                                             │
│ ValidateIdentifier(input) ──► Rejects anything failing ^[a-zA-Z0-9_]+$                 │
│                                                                                        │
│ Layer 2: PostgreSQL Identifier Quoting                                                 │
│ pq.QuoteIdentifier(domainDB) & pq.QuoteLiteral(pass)                                   │
│ Treats SQL object names strictly as literal, escaped identifiers.                      │
└────────────────────────────────────────────────────────────────────────────────────────┘
```

1. **Layer 1: Whitelist Regex Validation**: Before executing any DDL, `infra-provisioner` validates `tenant_id`, `domainDB`, and `user` using `regexp.MustCompile("^[a-zA-Z0-9_]+$")`. Payloads containing semicolons (`;`), spaces (` `), or quotes (`'`) fail validation and are immediately rejected.
2. **Layer 2: Identifier Quoting (`pq.QuoteIdentifier`)**: Wraps database names and role names in double quotes (`"order_db"`) and uses `pq.QuoteLiteral` for passwords, preventing query breakout even if exotic unicode characters pass initial checks.

---

## 6. Supply Chain Hijacking Defense: Distroless & Zero-Dependency Hardening

Because `infra-provisioner` mounts `/var/run/docker.sock`, a malicious third-party Go package (typosquatted dependency or compromised transitive package) could execute an `init()` payload to query the Docker socket and spawn host-takeover scripts.

### 1. Minimal Dependency Footprint
The `infra-provisioner` Go module imports **zero framework, logging, or third-party bloat**. It relies strictly on:
- Official Docker SDK (`github.com/docker/docker`)
- Official RabbitMQ Client (`github.com/rabbitmq/amqp091-go`)
- Pure PostgreSQL Driver (`github.com/lib/pq`)
- Go Standard Library (`crypto`, `database/sql`, `regexp`, `encoding/json`)

### 2. Multi-Stage Distroless Runtime Image (`infra-provisioner/Dockerfile`)
Standard Linux images (`alpine`, `ubuntu`) contain `/bin/sh`, `ash`, `curl`, `wget`, and OS utilities. If a compromised package attempts a shell execution payload (`exec.Command("/bin/sh", "-c", "curl ...")`), standard images execute it.

We compile `infra-provisioner` into a **Distroless Runtime Image** (`gcr.io/distroless/static-debian12:nonroot`):

```dockerfile
FROM golang:1.22-alpine AS builder
WORKDIR /app
COPY go.mod go.sum* ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o infra-provisioner ./cmd/main.go

# Distroless Runtime Image (Zero Shell, Zero OS Utilities, Non-Root)
FROM gcr.io/distroless/static-debian12:nonroot
WORKDIR /
COPY --from=builder /app/infra-provisioner /infra-provisioner
USER nonroot:nonroot
ENTRYPOINT ["/infra-provisioner"]
```

> **Security Guarantee:**
> Distroless containers contain **no shell (`/bin/sh`)**, **no package manager (`apk`, `apt`)**, and **no OS binaries**. If a malicious dependency attempts `exec.Command("/bin/sh")`, the operating system call fails immediately with `exec: "/bin/sh": stat /bin/sh: no such file or directory`.

---

## 7. Environment Variable Dumping Defense: Memory-Backed File Secrets (`/run/secrets/`)

Environment variables (`os.Getenv`) in containerized environments are vulnerable to process inspection (e.g. `/proc/1/environ` or `docker inspect`).

To protect master keys, `infra-provisioner` implements **File-Based Secret Mount Fallbacks** (supporting Docker Secrets, Kubernetes Secrets, and Vault Agent tmpfs mounts):

```go
func getSecret(envKey, secretFilePath, fallback string) string {
    // 1. Check for explicit file environment variable (e.g. INFRA_MASTER_SECRET_FILE)
    if filePath, exists := os.LookupEnv(envKey + "_FILE"); exists {
        if content, err := os.ReadFile(strings.TrimSpace(filePath)); err == nil {
            return strings.TrimSpace(string(content))
        }
    }
    // 2. Check default memory-backed secret mount path (/run/secrets/...)
    if secretFilePath != "" {
        if content, err := os.ReadFile(secretFilePath); err == nil {
            return strings.TrimSpace(string(content))
        }
    }
    // 3. Fallback to standard environment variable
    if value, exists := os.LookupEnv(envKey); exists {
        return strings.TrimSpace(value)
    }
    return fallback
}
```

---

## 8. Architecture Security Comparison Matrix

| Evaluation Metric | Option B (Multi-Container) | Flawed Proposal (Root in App) | Declarative Bootstrapping |
| :--- | :--- | :--- | :--- |
| **Container Count** | 5+ Containers (High Resource Drain) | 1 Container | **1 Container (Optimal Footprint)** |
| **Clean Architecture** | Respected | Respected | **100% Respected (Config-Driven)** |
| **Secret Safety** | Isolated | Compromised (App holds root key) | **100% Isolated** |
| **Role Security** | Engine-level | Bypassed by root key | **Enforced at Database Role Level** |
| **DDL SQLi Protection** | None | None | **Whitelist Regex & `pq.QuoteIdentifier`** |
| **Supply Chain Hardening**| Standard Image | Standard Image | **Distroless (No `/bin/sh`, Non-Root)** |
| **Env Var Leak Protection**| Plaintext Env Vars | Plaintext Env Vars | **File Secret Mounts (`/run/secrets/`)** |
| **Adding New Services** | Must change infrastructure | Must rewrite application code | **Update JSON config map / Secret file** |