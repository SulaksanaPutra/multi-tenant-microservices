#!/usr/bin/env bash
#
# clean-e2e-data.sh
# ---------------------------------------------------------------------------
# Reset every database / broker / container artifact that the E2E test suite
# pollutes, WITHOUT tearing down the shared infrastructure or the microservice
# containers. Run this after `(cd e2e-tests && CGO_ENABLED=0 go test -v ./...)`
# to leave the platform in a pristine, repeatable state.
#
# What it clears:
#   * auth_db            -> credentials, memberships, refresh tokens, setup
#                           tokens, roles, role_permissions, user_roles,
#                           user_permission_versions
#   * user_db            -> users, memberships, inbox, outbox
#   * notification_db    -> notifications, inbox
#   * tenant_manager_db  -> tenants, tenant_infrastructures, outbox, inbox
#   * shared_db          -> all dynamically provisioned tnt_*_order_db schemas
#   * dedicated tenant DB containers (postgres-tenant-*)
#   * Mailpit inbox
#   * RabbitMQ queues (best-effort, via Management HTTP API)
#
# It does NOT touch the `permissions` catalog in auth_db: domain services
# re-register those capabilities at boot, and no E2E test mutates them.
# ---------------------------------------------------------------------------

set -euo pipefail

PGHOST="${PGHOST:-localhost}"
PGPORT="${PGPORT:-5432}"
PGUSER="${PGUSER:-postgres}"
PGPASSWORD="${PGPASSWORD:-postgres}"
MAILPIT_DASHBOARD_PORT="${MAILPIT_DASHBOARD_PORT:-8025}"
RABBITMQ_MGMT_URL="${RABBITMQ_MGMT_URL:-http://localhost:15672}"
RABBITMQ_USER="${RABBITMQ_USER:-guest}"
RABBITMQ_PASS="${RABBITMQ_PASS:-guest}"

export PGPASSWORD

psql_cmd() { # psql_cmd <dbname> <sql>
  local db="$1" sql="$2"
  psql -h "$PGHOST" -p "$PGPORT" -U "$PGUSER" -d "$db" -v ON_ERROR_STOP=1 -c "$sql" >/dev/null
}

echo "[clean-e2e-data] 1/6 Cleaning auth_db ..."
psql_cmd auth_db '
  TRUNCATE TABLE public.user_credentials,
                   public.user_tenant_memberships,
                   public.refresh_tokens,
                   public.password_setup_tokens,
                   public.roles,
                   public.role_permissions,
                   public.user_roles,
                   public.user_permission_versions,
                   public.inbox RESTART IDENTITY CASCADE;
'

echo "[clean-e2e-data] 2/6 Cleaning user_db ..."
psql_cmd user_db '
  TRUNCATE TABLE public.users,
                   public.user_tenant_memberships,
                   public.inbox,
                   public.outbox RESTART IDENTITY CASCADE;
'

echo "[clean-e2e-data] 3/6 Cleaning notification_db ..."
psql_cmd notification_db '
  TRUNCATE TABLE public.notifications,
                   public.inbox RESTART IDENTITY CASCADE;
'

echo "[clean-e2e-data] 4/6 Cleaning tenant_manager_db ..."
psql_cmd tenant_manager_db '
  TRUNCATE TABLE public.tenants,
                   public.tenant_infrastructures,
                   public.outbox,
                   public.inbox RESTART IDENTITY CASCADE;
'

echo "[clean-e2e-data] 5/6 Dropping dynamic shared_db tenant schemas ..."
psql -h "$PGHOST" -p "$PGPORT" -U "$PGUSER" -d shared_db -v ON_ERROR_STOP=1 -c "
DO \$\$
DECLARE
    s text;
BEGIN
    FOR s IN
        SELECT schema_name
        FROM information_schema.schemata
        WHERE schema_name LIKE 'tnt\\_%\\_order_db'
    LOOP
        EXECUTE format('DROP SCHEMA IF EXISTS %I CASCADE', s);
    END LOOP;
END
\$\$;" >/dev/null

echo "[clean-e2e-data] Removing dedicated tenant DB containers ..."
# Best-effort: no containers may exist on a clean system.
docker rm -fv $(docker ps -aq --filter name=postgres-tenant-) 2>/dev/null || true

echo "[clean-e2e-data] Purging Mailpit inbox ..."
curl -s -X DELETE "http://localhost:${MAILPIT_DASHBOARD_PORT}/api/v1/messages" -o /dev/null || true

echo "[clean-e2e-data] Purging RabbitMQ queues (best-effort) ..."
# Best-effort: the Management API may be unavailable/disabled.
rabbit_purge() {
  local queue="$1" encoded
  encoded=$(python3 -c 'import sys,urllib.parse; print(urllib.parse.quote(sys.argv[1], safe=""))' "$queue" 2>/dev/null || echo "$queue")
  curl -s -u "${RABBITMQ_USER}:${RABBITMQ_PASS}" \
    -X DELETE "${RABBITMQ_MGMT_URL}/api/queues/%2F/${encoded}/contents" \
    -o /dev/null || true
}
for q in infra_provisioner_workspace_initiated \
         notification_service_user_created \
         notification_service_workspace_ready \
         order_service_infrastructure_provisioned \
         tenant_service_order_db_ready \
         user_service_workspace_initiated \
         auth_service_user_created_membership \
         auth_service_user_created_membership_dlq; do
  rabbit_purge "$q"
done

echo "[clean-e2e-data] Done. Platform is clean and ready for the next run."
