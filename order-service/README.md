# Archetype: A (API / Domain Service - Data Plane)

## Overview
`order-service` operates as the primary Data Plane service handling tenant domain workflows, order management, and schema-isolated data access.

## Architectural Bounds
- **Category:** Archetype A (API / Domain Service - Data Plane)
- **Layout:** `cmd/` -> `internal/{handler, service, repository, domain}`
- **Responsibilities:** Order lifecycle management, singleflight caching, multi-tenant DB connections, domain events.
