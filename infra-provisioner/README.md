# Archetype: C (Isolated Infrastructure Provisioner)

## Overview
`infra-provisioner` is an isolated, non-web background worker that listens for infrastructure task requests and orchestrates Docker containers / tenant databases.

## Architectural Bounds
- **Category:** Archetype C (Isolated Infrastructure Provisioner)
- **Layout:** `cmd/` -> `internal/{worker, provisioner, infrastructure, domain}`
- **Prohibited:** Web frameworks, Gin, tenant web middleware, HTTP transport abstractions.
- **Responsibilities:** Docker SDK integration, QoS=1 AMQP queue worker, transactional DDL execution, isolated container orchestration.
