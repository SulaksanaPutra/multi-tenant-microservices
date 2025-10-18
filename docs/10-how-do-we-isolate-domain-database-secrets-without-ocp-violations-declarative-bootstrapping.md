# Domain Database Secret Isolation and Declarative Bootstrapping

*Preventing root credential distribution, enforcing PostgreSQL role-level least privilege, and declarative container provisioning.*

---

## 1. The Architectural Dilemma: Root Keys vs Container Sprawl

When hosting dedicated tenant containers (`postgres-tenant-<id>`) that serve multiple domain microservices (`order-service`, `billing-service`, `inventory-service`), infrastructure design faces an isolation trade-off:

```text
               [ ARCHITECTURAL TRADEOFF ]

Option A: Container Sprawl          Option B: Root Key Leakage
Dedicated Postgres per Domain       Domain Services Hold Root Superuser Secrets
- 50 Tenants x 5 Domains = 250 DBs  - High blast radius if an app is compromised
- Excessive memory and CPU overhead - Bypasses role-level access boundaries
```

### The Root Key Distribution Trap
A naive approach might let each domain service (`order-service`) connect to the tenant container as `postgres` superuser on boot, creating its own database and roles.

This introduces a serious vulnerability:
- If `order-service` runs initial root DDL setup, it must hold `INFRA_MASTER_SECRET` (the container root password).
- If an attacker exploits an RCE or injection vulnerability inside `order-service`, they gain root superuser access to the container, bypassing role-level boundaries and accessing databases belonging to other domains.

**Core Design Invariant**: Application containers must **never** receive infrastructure root superuser credentials.

---

## 2. Declarative Bootstrapping Pattern

To enforce role-level least privilege without manual intervention per microservice, we implement **Declarative Bootstrapping** managed by `infra-provisioner`:

```text
┌────────────────────────────────────────────────────────────────────────────────────────┐
│ INFRASTRUCTURE LAYER (infra-provisioner - Isolated Worker, No Public Ports)            │
│ Holds:                                                                                 │
│   - INFRA_MASTER_SECRET (Root container password)                                      │
│   - DOMAIN_SECRETS = {"order_db": "sec_123", "inventory_db": "sec_456"}                │
│                                                                                        │
│ 1. Boots container postgres-tenant-abc with POSTGRES_PASSWORD=Derive(INFRA_SECRET)     │
│ 2. Iterates over DOMAIN_SECRETS configuration:                                         │
│    - Creates 'order_db' and 'order_user' with PASSWORD = Derive("sec_123", tenantID)   │
│    - Creates 'inventory_db' and 'inventory_user' with Derive("sec_456", tenantID)      │
└──────────────────────────┬─────────────────────────────────────────────────────────────┘
                           │
                           ▼
┌────────────────────────────────────────────────────────────────────────────────────────┐
│ DATA PLANE LAYER (order-service - Application Container)                               │
│ Holds only:                                                                            │
│   - ORDER_SERVICE_SECRET ("sec_123")                                                   │
│                                                                                        │
│ 1. Connects strictly as 'order_user' with password derived from ORDER_SERVICE_SECRET    │
│ 2. Zero visibility into INFRA_MASTER_SECRET or other domain databases                   │
└────────────────────────────────────────────────────────────────────────────────────────┘
```

1. **`infra-provisioner`** receives `INFRA_MASTER_SECRET` and a configuration map of domain database specifications.
2. During container setup, it connects as root `postgres`, creates the domain databases, and assigns dedicated roles with restricted privileges.
3. **`order-service`** only receives `ORDER_SERVICE_SECRET`. It connects strictly as `order_user` to `order_db`, with no permissions on other databases in the container.

---

## 3. Safe Dynamic SQL Execution

When provisioning databases and users programmatically, database names and user roles cannot be supplied via standard parameterized query parameters (`$1`).

To prevent SQL injection during dynamic DDL execution:

```go
var validIdentifier = regexp.MustCompile(`^[a-zA-Z0-9_]+$`)

func ValidateIdentifier(name string) error {
    if !validIdentifier.MatchString(name) {
        return fmt.Errorf("invalid SQL identifier: %s", name)
    }
    return nil
}

func CreateDatabaseAndUser(ctx context.Context, tx *sql.Tx, dbName, username, password string) error {
    if err := ValidateIdentifier(dbName); err != nil {
        return err
    }
    if err := ValidateIdentifier(username); err != nil {
        return err
    }

    // Identifiers quoted; password supplied safely via string escaping
    createUserSQL := fmt.Sprintf("CREATE USER %s WITH PASSWORD %s;", 
        pq.QuoteIdentifier(username), 
        pq.QuoteLiteral(password),
    )
    if _, err := tx.ExecContext(ctx, createUserSQL); err != nil {
        return fmt.Errorf("failed to create user: %w", err)
    }

    createDBSQL := fmt.Sprintf("CREATE DATABASE %s OWNER %s;", 
        pq.QuoteIdentifier(dbName), 
        pq.QuoteIdentifier(username),
    )
    if _, err := tx.ExecContext(ctx, createDBSQL); err != nil {
        return fmt.Errorf("failed to create database: %w", err)
    }

    return nil
}
```

By combining strict regex whitelisting with `pq.QuoteIdentifier` and `pq.QuoteLiteral`, dynamic DDL execution remains safe against injection attacks.

---

## 4. Architectural Invariants & Operational Trade-offs

- **Root Credential Isolation**: Application data-plane containers operate under strictly bounded PostgreSQL roles with zero superuser access.
- **Declarative Schema Growth**: New domain services can be provisioned into tenant containers via configuration maps without modifying infrastructure code.
- **Identifier Defense-in-Depth**: All dynamic SQL identifiers enforce regex validation and quoting before query execution.
