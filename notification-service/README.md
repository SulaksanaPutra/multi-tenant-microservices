# Archetype: B (Event-Driven Consumer)

## Overview
`notification-service` is an asynchronous event consumer responsible for processing system notifications and delivering emails via SMTP.

## Architectural Bounds
- **Category:** Archetype B (Event-Driven Consumer)
- **Layout:** `cmd/` -> `internal/{consumer, service, domain, infrastructure}`
- **Prohibited:** Web frameworks, HTTP controllers (`internal/handler`), unnecessary DB repositories.
- **Responsibilities:** AMQP consumer queue listeners, idempotent message handling, Mailer adapter integration (SMTP/Mailpit).
