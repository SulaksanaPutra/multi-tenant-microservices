# Archetype: A (API / Domain Service - Control Plane)

## Overview
`tenant-service` acts as the Control Plane for multi-tenant workspace orchestration and database provisioning coordination.

## Architectural Bounds
- **Category:** Archetype A (API / Domain Service - Control Plane)
- **Layout:** `cmd/` -> `internal/{handler, service, repository, domain}`
- **Responsibilities:** Workspace registration, tenant metadata management, Outbox event generation (`workspace.initiated`), tenant DB routing activation.
