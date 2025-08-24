# Master Execution Roadmap

This document outlines the step-by-step implementation tasks for building and running the **Polyrepo Microservices Project**.

---

## 🚦 Status Legend
*   `[x]` **Finished / Completed**
*   `[ ]` **Pending / To Do**

---

## 📋 Execution Plan

### Phase 1: Infrastructure Setup (`broker/`)
> **Services Spun Up**: PostgreSQL, RabbitMQ (with Management UI), Mailpit (Mock SMTP Mailer), Traefik (Gateway Reverse Proxy & Dashboard).

- [x] **Step 1.1**: Create `broker/` directory.
- [x] **Step 1.2**: Write `broker/init.sql` containing base DDL for `public.users` and `public.notifications` tables.
- [x] **Step 1.3**: Write `broker/docker-compose.yml` to define PostgreSQL (mounting `./init.sql` into `/docker-entrypoint-initdb.d/init.sql`), RabbitMQ, Mailpit, Traefik, and the `broker-network` network.
- [x] **Step 1.4**: Run `docker compose up -d` in `broker/` and verify container health, base schema initialization, & web dashboards (RabbitMQ: 15672, Mailpit: 8025, Traefik: 8080).

---

### Phase 2: User Service (`user-service/`)
> **Service Spun Up**: `user-service` (Go REST API container with Traefik routing labels).

- [x] **Step 2.1**: Initialize Go module (`go mod init user-service`) and folder layout (`cmd/main.go`, `internal/`).
- [x] **Step 2.2**: Implement PostgreSQL client connecting to `public` schema (`users` table).
- [x] **Step 2.3**: Implement RabbitMQ publisher to send `UserRegistered` event to exchange `company.events`.
- [x] **Step 2.4**: Implement HTTP API handler (`POST /api/v1/register`) returning `202 Accepted`.
- [x] **Step 2.5**: Create `user-service/Dockerfile` with Traefik auto-discovery labels (`PathPrefix("/api/v1/register")`).

---

### Phase 3: Tenant Service (`tenant-service/`)
> **Service Spun Up**: `tenant-service` (Go Background Worker container).

- [x] **Step 3.1**: Initialize Go module (`go mod init tenant-service`) and folder layout.
- [x] **Step 3.2**: Add SQL migration templates for dynamic tenant sub-schemas under `migrations/`.
- [x] **Step 3.3**: Implement RabbitMQ consumer for `UserRegistered` event on queue `tenant_service_user_registered`.
- [x] **Step 3.4**: Implement Dynamic Schema Provisioner (`CREATE SCHEMA tenant_<id>`, execute migration scripts, seed default tenant data).
- [x] **Step 3.5**: Implement RabbitMQ publisher to send `TenantProvisioned` event.
- [x] **Step 3.6**: Create `tenant-service/Dockerfile`.

---

### Phase 4: Notification Service (`notification-service/`)
> **Service Spun Up**: `notification-service` (Go Background Worker container).

- [x] **Step 4.1**: Initialize Go module (`go mod init notification-service`) and folder layout.
- [x] **Step 4.2**: Implement database client for logging entries into `notifications` table.
- [x] **Step 4.3**: Implement SMTP client connecting to Mailpit.
- [x] **Step 4.4**: Implement RabbitMQ consumer for `TenantProvisioned` event on queue `notification_service_tenant_provisioned`.
- [x] **Step 4.5**: Trigger welcome email via Mailpit and write notification audit log upon consuming event.
- [x] **Step 4.6**: Create `notification-service/Dockerfile`.

---

### Phase 5: End-to-End Integration & Verification
- [x] **Step 5.1**: Launch all infrastructure and 3 microservice containers attached to `broker-network`.
- [x] **Step 5.2**: Send test HTTP registration request via Traefik Gateway (`POST http://localhost/api/v1/register`).
- [x] **Step 5.3**: Verify user creation in PostgreSQL `public.users` table.
- [x] **Step 5.4**: Verify `UserRegistered` event in RabbitMQ Management UI (`http://localhost:15672`).
- [x] **Step 5.5**: Verify dynamic creation of `tenant_<id>` schema and tables in PostgreSQL.
- [x] **Step 5.6**: Verify `TenantProvisioned` event consumption in RabbitMQ UI.
- [x] **Step 5.7**: Verify notification row in database and view welcome email in Mailpit Web Dashboard (`http://localhost:8025`).
- [x] **Step 5.8**: Verify Traefik auto-discovered routes in Traefik Dashboard (`http://localhost:8080`).
