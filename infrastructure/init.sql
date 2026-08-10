-- Creates the core control-plane databases only.
-- Service-owned schemas are applied by per-service goose migrations at boot:
--   * auth_db            -> auth-service/migrations
--   * user_db            -> user-service/migrations
--   * tenant_manager_db  -> tenant-service/migrations
--   * notification_db    -> notification-service/migrations
-- shared_db is created via POSTGRES_DB; order-service provisions dynamic
-- tenant schemas at runtime.
-- NOTE: docker-entrypoint-initdb.d only runs on fresh volumes.

CREATE DATABASE user_db;
CREATE DATABASE auth_db;
CREATE DATABASE tenant_manager_db;
CREATE DATABASE notification_db;
CREATE DATABASE payment_db;

