# How Do We Isolate Container Orchestration & Prevent Host Takeover? The Infra-Provisioner Pattern

*An Engineering Deep Dive into Eliminating Privilege Escalation, Host Resource Starvation, and Password Leakage using Dedicated Infrastructure Workers, QoS Consumer Throttling, and Stateless HMAC-SHA256 Secret Derivation in Go*

---

## 1. The Vulnerability & Anti-Pattern: Data Plane Infrastructure Coupling

In early iterations of multi-tenant microservice architectures, application domain services (like `order-service`) are often tasked with dynamically provisioning containerized infrastructure when a new tenant registers under a **Dedicated Plan**.

To achieve this, the application container mounts the host's Docker socket (`/var/run/docker.sock`) and invokes the Docker Engine API directly upon receiving registration events.

```yaml
# THE VULNERABLE CONFIGURATION (order-service/docker-compose.yml)
services:
  order-service:
    build: .
    volumes:
      - /var/run/docker.sock:/var/run/docker.sock #  CATASTROPHIC SECURITY VULNERABILITY
```

### 1. Privilege Escalation & Host Takeover
Mounting `/var/run/docker.sock` inside an application container granting full root privileges over the host Docker daemon. If an attacker discovers a Remote Code Execution (RCE) vulnerability in `order-service` (e.g. via dependency injection or SQL injection), they can execute commands directly on the host Docker daemon, break out of the container, spawn privileged containers with host filesystem mounts (`-v /:/host`), and achieve **complete host takeover**.

### 2. Host Resource Starvation & Kernel OOM Kills
When dynamic container spawning runs inside an un-throttled asynchronous event consumer, there are no upper bounds or concurrency controls. An event burst (e.g. 50 registration events in 2 seconds) will cause `order-service` to invoke `ContainerCreate` concurrently for dozens of PostgreSQL containers. This instantly exhausts host RAM, CPU, and file descriptors, triggering kernel Out-Of-Memory (OOM) kills that crash the host node.

### 3. Clean Architecture & Bounded Context Violation
From a **Clean Architecture** perspective, placing container orchestration inside `order-service` is a direct violation of the **Single Responsibility Principle (SRP)** and **Bounded Context boundaries**. A domain service responsible for managing order data and business rules should never be responsible for host-level container provisioning or system administration tasks.

---

## 2. The Solution: The `infra-provisioner` Microservice Pattern

To resolve both the security and system reliability risks, we extracted container orchestration out of the data plane into a dedicated, isolated Go microservice: `infra-provisioner`.

```
                               ┌───────────────────────────────────┐
                               │          TENANT-SERVICE           │
                               │      (Control Plane Registry)     │
                               └─────────────────┬─────────────────┘
                                                 │
                                      WorkspaceInitiated Event
                                                 │
                                                 ▼
                               ┌───────────────────────────────────┐
                               │         INFRA-PROVISIONER         │
                               │   (Isolated Infrastructure Worker)│
                               │  - Mounts /var/run/docker.sock    │
                               │  - QoS Prefetch = 1               │
                               │  - Enforces 512MB / 0.5 CPU limits│
                               └─────────────────┬─────────────────┘
                                                 │
                                  InfrastructureProvisioned Event
                                      (Non-sensitive Metadata)
                                                 │
                                                 ▼
                               ┌───────────────────────────────────┐
                               │           ORDER-SERVICE           │
                               │        (Domain Data Plane)        │
                               │  - Derives password statelessly   │
                               │  - Executes SQL Migrations        │
                               └─────────────────┬─────────────────┘
                                                 │
                                     TenantOrderDBReady Event
                                                 │
                                                 ▼
                               ┌───────────────────────────────────┐
                               │          TENANT-SERVICE           │
                               │   (Passively Activates Workspace) │
                               └───────────────────────────────────┘
```

### Key Architectural Isolation Rules
1. **Zero Public Inbound Ports**: `infra-provisioner` has no HTTP API handlers and is **not exposed** to the Traefik API gateway. It operates strictly as an internal RabbitMQ queue consumer.
2. **Socket Isolation**: `/var/run/docker.sock` is removed from `order-service` entirely and mounted *exclusively* to `infra-provisioner`.
3. **Data Plane Decoupling**: `order-service` returns to being a pure domain service that only interacts with SQL connection abstractions, completely oblivious to whether PostgreSQL is running on Docker, AWS RDS, or bare metal.

---

## 3. QoS Throttling & Host Resource Enclosure

Spawning infrastructure dynamically requires strict upper bounds to protect the host kernel from starvation.

### 1. Consumer QoS Prefetch Throttling
Inside `infra-provisioner`, the RabbitMQ consumer enforces a strict Quality of Service (QoS) limit:

```go
// Enforcing strict prefetch throttling in infra-provisioner
if err := params.Client.Channel.Qos(1, 0, false); err != nil {
    return nil, fmt.Errorf("failed to set Channel QoS prefetch count: %w", err)
}
```
Setting `prefetchCount = 1` guarantees that the worker pulls and processes registration events sequentially. If 100 registration events hit the queue, containers are provisioned one by one, preventing host spikes.

### 2. Container HostConfig Resource Bounds
When `infra-provisioner` invokes the Docker API to create a new dedicated PostgreSQL container, it injects hard resource bounds inside `container.HostConfig`:

```go
// Enforcing container memory and CPU limits
hostConfig := &container.HostConfig{
    RestartPolicy: container.RestartPolicy{Name: "always"},
    Resources: container.Resources{
        Memory:   512 * 1024 * 1024, // 512MB hard RAM limit per tenant DB
        NanoCPUs: 500000000,         // 0.5 CPU cores hard limit per tenant DB
    },
}
```

### 3. Active Container Health Checks (`pg_isready`)
Docker reporting a container state as `"running"` only indicates that the container process started. PostgreSQL internal startup routines (`initdb`, socket binding, WAL setup) take several seconds.

`infra-provisioner` runs an active `pg_isready` polling loop before emitting completion events, eliminating race conditions:

```go
func (p *DockerProvisioner) waitForPostgresReady(ctx context.Context, host string, port int, user, password, dbName string) error {
    dsn := fmt.Sprintf("host=%s port=%d user=%s password=%s dbname=%s sslmode=disable", host, port, user, password, dbName)

    for i := 0; i < 15; i++ {
        db, err := sql.Open("postgres", dsn)
        if err == nil {
            pingCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
            err = db.PingContext(pingCtx)
            cancel()
            _ = db.Close()
            if err == nil {
                return nil // PostgreSQL is healthy!
            }
        }
        time.Sleep(2 * time.Second)
    }
    return fmt.Errorf("timed out waiting for postgres at %s:%d after 30s", host, port)
}
```

---

## 4. Zero-Trust Credentials: Stateless HMAC-SHA256 Secret Derivation

Transmitting raw database connection strings containing plaintext passwords across RabbitMQ message payloads is a severe security hazard. Anyone with access to the message broker management interface or queue logs can inspect raw database passwords.

We eliminate password transmission using **Stateless HMAC-SHA256 Secret Derivation**.

### 1. Shared Master Secret
Both `infra-provisioner` and `order-service` share a master secret key (`SHARED_DB_SECRET`) injected via environment variables.

### 2. Deterministic Derivation Algorithm (`internal/crypto/credentials.go`)
Both services derive tenant passwords statelessly using the exact same function:

```go
package crypto

import (
    "crypto/hmac"
    "crypto/sha256"
    "encoding/hex"
    "fmt"
)

// DeriveTenantDBPassword computes a deterministic, collision-resistant password for a tenant database.
func DeriveTenantDBPassword(secret, tenantID string) string {
    h := hmac.New(sha256.New, []byte(secret))
    h.Write([]byte("tenant_db_v1_" + tenantID))
    return fmt.Sprintf("pg_%s", hex.EncodeToString(h.Sum(nil))[:24])
}
```

### 3. Non-Sensitive Event Payload
When `infra-provisioner` finishes container startup and health checks, it publishes `infrastructure.provisioned` over RabbitMQ with **zero sensitive data**:

```json
{
  "event_id": "evt_98765",
  "tenant_id": "tenant-abc",
  "plan": "dedicated",
  "db_host": "postgres-tenant-abc",
  "db_port": 5432,
  "db_name": "postgres",
  "db_user": "postgres",
  "schema_name": "public"
}
```

### 4. Stateless Password Reconstruction
Upon consuming `infrastructure.provisioned`, `order-service` derives the exact same password in memory:
```go
password := crypto.DeriveTenantDBPassword(c.sharedSecret, evt.TenantID)
dsn := fmt.Sprintf("host=%s port=%d user=%s password=%s dbname=%s sslmode=disable",
    evt.DBHost, evt.DBPort, evt.DBUser, password, evt.DBName)
```
This guarantees **Zero Secret Transmission** over network message brokers.

---

## 5. Unified Gateway Routing & Event Choreography

To maintain Clean Architecture, `infra-provisioner` acts as a unified routing gateway for all hosting strategies, while synchronous HTTP write-backs are completely eliminated.

### 1. Dual-Plan Gateway Routing
When `infra-provisioner` consumes `workspace.initiated`:
* **`shared` plan**: Instantly publishes `infrastructure.provisioned` pointing to the shared database host and computed schema name (`<tenant_id>_order_db`). No Docker container is created.
* **`dedicated` plan**: Provisions the Docker container with resource bounds, waits for `pg_isready`, and publishes `infrastructure.provisioned` pointing to the dedicated container hostname.

This allows `order-service` to consume a **single event** (`infrastructure.provisioned`) for all tenant migrations, regardless of the underlying hosting strategy.

### 2. Elimination of Synchronous HTTP Write-Backs
Previously, application workers made synchronous HTTP `PATCH` calls back to `tenant-service`. If `tenant-service` was down during migration completion, the HTTP call failed, causing split-brain states.

We replaced synchronous callbacks with **pure event choreography**:
1. `infra-provisioner` emits `infrastructure.provisioned`.
2. `order-service` consumes the event, executes SQL schema migrations (`001_create_orders.sql`), and emits `tenant.order_db.ready`.
3. `tenant-service` consumes `tenant.order_db.ready` over RabbitMQ to update control plane state and activate the workspace asynchronously.

---

## 6. Architecture Comparison & Benefits

| Dimension | Legacy Pattern (`order-service` docker) | Refactored `infra-provisioner` Pattern | Architectural Gain |
| :--- | :--- | :--- | :--- |
| **Docker Socket** | Mounted in application container | Mounted *only* in `infra-provisioner` | Prevents privilege escalation & host takeover |
| **Inbound Ports** | Exposed via Traefik Gateway | **Zero public ports** (internal worker) | Eliminates attack vector for infrastructure API |
| **Resource Limits** | Unbounded container creation | `512MB RAM`, `0.5 CPU` per container | Prevents host memory & CPU exhaustion |
| **Consumer Control**| Un-throttled event handling | Strict QoS `prefetchCount = 1` | Prevents event-burst kernel OOM kills |
| **Broker Security** | Plaintext credentials in payloads | Stateless `HMAC-SHA256` derivation | Zero password transmission across broker |
| **Choreography** | Synchronous HTTP callbacks | Pure asynchronous event streams | Prevents split-brain on network failures |
| **Clean Architecture**| Leaked Docker DDL into application | Application handles pure SQL abstractions | Strict SRP & Bounded Context adherence |
