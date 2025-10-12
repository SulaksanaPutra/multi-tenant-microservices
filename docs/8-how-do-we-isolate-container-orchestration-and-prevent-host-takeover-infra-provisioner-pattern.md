# Container Orchestration Isolation and the Infra-Provisioner Pattern

*Preventing host compromise, controlling resource consumption, and isolating Docker daemon privileges in multi-tenant environments.*

---

## 1. The Vulnerability: Mounting Docker Sockets in Domain Services

When implementing on-demand dedicated database provisioning, a naive implementation might mount the host Docker socket (`/var/run/docker.sock`) directly inside an application container:

```yaml
# Insecure configuration: exposing host Docker socket to business service
services:
  order-service:
    image: order-service:latest
    volumes:
      - /var/run/docker.sock:/var/run/docker.sock # Catastrophic privilege escalation risk
```

### Security & Operational Risks:

1. **Privilege Escalation and Host Compromise**:
   Access to `/var/run/docker.sock` provides effective root access to the host machine. If an attacker identifies a Remote Code Execution (RCE) vulnerability or arbitrary command injection in `order-service`, they can communicate directly with the Docker daemon, create privileged containers mounting the host root filesystem (`-v /:/host`), and compromise the entire host node.

2. **Uncontrolled Resource Consumption**:
   Without rate-limiting or concurrency control, an unexpected burst of registrations could trigger dozens of simultaneous `ContainerCreate` calls, exhausting CPU, memory, and file descriptors on the host.

3. **Violation of Bounded Contexts**:
   Business domain services responsible for processing orders should not contain container management logic or system administration dependencies.

---

## 2. The Solution: Dedicated `infra-provisioner` Microservice

To mitigate these risks, all Docker daemon interaction is isolated into a dedicated service: `infra-provisioner`.

```text
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
                               │   (Dedicated Infrastructure Agent)│
                               │  - Mounts /var/run/docker.sock    │
                               │  - QoS Prefetch = 1               │
                               │  - Enforces container limits      │
                               └─────────────────┬─────────────────┘
                                                 │
                                  InfrastructureProvisioned Event
                                      (Network Metadata Only)
                                                 │
                                                 ▼
                               ┌───────────────────────────────────┐
                               │           ORDER-SERVICE           │
                               │        (Domain Data Plane)        │
                               │  - Derives password statelessly   │
                               │  - Executes SQL Migrations        │
                               └───────────────────────────────────┘
```

---

## 3. Core Protection Mechanisms

### 1. Attack Surface Reduction
`infra-provisioner` has no public HTTP endpoints and does not accept user-facing traffic. It only consumes vetted AMQP task messages from an internal queue, preventing external HTTP injection attacks from reaching the container that holds Docker privileges.

### 2. Concurrency Throttling via QoS Prefetch
To prevent container bursts from overwhelming host resources, `infra-provisioner` sets its AMQP consumer prefetch count to 1:

```go
err := channel.Qos(
    1,     // prefetch count: process exactly one task at a time
    0,     // prefetch size
    false, // global
)
```

This enforces sequential provisioning. If 50 workspaces are registered simultaneously, tasks queue in RabbitMQ and are provisioned one after another without crashing the host.

### 3. Container Resource Bounds
Every spawned container has strict CPU and memory limits applied through the Docker SDK:

```go
hostConfig := &container.HostConfig{
    Resources: container.Resources{
        Memory:   512 * 1024 * 1024, // 512MB RAM ceiling
        NanoCPUs: 500000000,          // 0.5 CPU core maximum
    },
    RestartPolicy: container.RestartPolicy{
        Name: "unless-stopped",
    },
}
```

---

## 4. Architectural Invariants & Operational Trade-offs

- **Daemon Privilege Isolation**: Only `infra-provisioner` mounts the Docker socket; business domain services operate without host container privileges.
- **Controlled Concurrency**: AMQP QoS Prefetch=1 serializes container spawning, preventing host CPU and memory exhaustion during registration spikes.
- **Resource Limits**: All tenant database containers enforce hard CPU and RAM bounds.
