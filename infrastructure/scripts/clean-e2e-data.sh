#!/usr/bin/env bash
#
# clean-e2e-data.sh
# ---------------------------------------------------------------------------
# Reset every database / broker / container artifact that the E2E test suite
# pollutes, WITHOUT tearing down the shared infrastructure or the microservice
# containers. Run this after `(cd e2e-tests && CGO_ENABLED=0 go test -v ./...)`
# to leave the platform in a pristine, repeatable state.
#
# Tier-aware: the deployment tier determines which postgres instance hosts each
# database. Lite/standard share the control-plane `postgres` container on the
# host port PGPORT (5432), while premium runs one dedicated container per
# service, each exposed on its own host port:
#   tenant_manager_db -> tenant-db     (5433)
#   user_db           -> user-db       (5434)
#   shared_db         -> data-plane-db (5435)
#   auth_db           -> auth-db       (5436)
#   notification_db   -> notification-db (5437)
#   payment_db        -> payment-db      (5438)
# The tier is taken from --tier <tier>, else $TIER, else infrastructure/.env.
#
# What it clears:
#   * auth_db            -> credentials, memberships, refresh tokens, setup
#                           tokens, roles, role_permissions, user_roles,
#                           user_permission_versions
#   * user_db            -> users, memberships, inbox, outbox
#   * notification_db    -> notifications, inbox
#   * payment_db         -> payments, payment_attempts, payment_tenant_configs,
#                           payment_outbox, payment_inbox
#   * tenant_manager_db  -> tenants, tenant_infrastructures, outbox, inbox
#   * shared_db          -> all dynamically provisioned tnt_*_order_db schemas
#   * dedicated tenant DB containers (postgres-tenant-*)
#   * RabbitMQ queues
#   * Mailpit inbox
#
# It does NOT touch the `permissions` catalog in auth_db: domain services
# re-register those capabilities at boot, and no E2E test mutates them.
# ---------------------------------------------------------------------------

set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
ENV_FILE="$ROOT/infrastructure/.env"

PGHOST="${PGHOST:-localhost}"
PGPORT="${PGPORT:-5432}"
PGUSER="${PGUSER:-postgres}"
PGPASSWORD="${PGPASSWORD:-postgres}"
MAILPIT_DASHBOARD_PORT="${MAILPIT_DASHBOARD_PORT:-8025}"
RABBITMQ_MGMT_URL="${RABBITMQ_MGMT_URL:-http://localhost:15672}"
RABBITMQ_USER="${RABBITMQ_USER:-guest}"
RABBITMQ_PASS="${RABBITMQ_PASS:-guest}"

export PGPASSWORD

# ---------------------------------------------------------------------------
# Tier detection
# ---------------------------------------------------------------------------

tier="${TIER:-}"
while [ "$#" -gt 0 ]; do
  case "$1" in
    --tier) tier="$2"; shift 2 ;;
    *) echo "[clean-e2e-data] Unknown argument: $1" >&2; exit 1 ;;
  esac
done

STATE_FILE="$ROOT/.active-tier"

if [ -z "$tier" ] && [ -f "$STATE_FILE" ]; then
  tier="$(tr -d '[:space:]' < "$STATE_FILE")"
fi
if [ -z "$tier" ] && [ -f "$ENV_FILE" ]; then
  tier="$(sed -n 's/^TIER=//p' "$ENV_FILE" | tr -d '[:space:]' | tail -1)"
fi
if [ -z "$tier" ] || [ "$tier" = "standard" ]; then
  if docker ps --format '{{.Names}}' 2>/dev/null | grep -qx 'auth-db'; then
    tier="premium"
  fi
fi
tier="${tier:-standard}"

# Resolve the host port each database listens on for the active tier.
# Lite/standard: every service DB lives inside the shared postgres container.
# Premium: one dedicated container per service (see infrastructure/docker-compose.yml,
# per-service-db profile).
db_port() {
  case "$1" in
    auth_db)           [ "$tier" = "premium" ] && echo 5436 || echo "$PGPORT" ;;
    user_db)           [ "$tier" = "premium" ] && echo 5434 || echo "$PGPORT" ;;
    tenant_manager_db) [ "$tier" = "premium" ] && echo 5433 || echo "$PGPORT" ;;
    notification_db)   [ "$tier" = "premium" ] && echo 5437 || echo "$PGPORT" ;;
    payment_db)        [ "$tier" = "premium" ] && echo 5438 || echo "$PGPORT" ;;
    shared_db)         [ "$tier" = "premium" ] && echo 5435 || echo "$PGPORT" ;;
    *) echo "$PGPORT" ;;
  esac
}

echo "[clean-e2e-data] Tier '$tier' — cleaning data-plane, control-plane and broker artifacts."

psql_cmd() { # psql_cmd <port> <dbname> <sql>
  local port="$1" db="$2" sql="$3"
  psql -h "$PGHOST" -p "$port" -U "$PGUSER" -d "$db" -v ON_ERROR_STOP=1 -c "$sql" >/dev/null
}

echo "[clean-e2e-data] 1/7 Cleaning auth_db ..."
psql_cmd "$(db_port auth_db)" auth_db '
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

echo "[clean-e2e-data] 2/7 Cleaning user_db ..."
psql_cmd "$(db_port user_db)" user_db '
  TRUNCATE TABLE public.users,
                   public.user_tenant_memberships,
                   public.inbox,
                   public.outbox RESTART IDENTITY CASCADE;
'

echo "[clean-e2e-data] 3/7 Cleaning notification_db ..."
psql_cmd "$(db_port notification_db)" notification_db '
  TRUNCATE TABLE public.notifications,
                   public.inbox RESTART IDENTITY CASCADE;
'

echo "[clean-e2e-data] 4/7 Cleaning payment_db ..."
psql_cmd "$(db_port payment_db)" payment_db '
  TRUNCATE TABLE public.payments,
                   public.payment_attempts,
                   public.payment_tenant_configs,
                   public.payment_outbox,
                   public.payment_inbox RESTART IDENTITY CASCADE;
'

echo "[clean-e2e-data] 5/7 Cleaning tenant_manager_db ..."
psql_cmd "$(db_port tenant_manager_db)" tenant_manager_db '
  TRUNCATE TABLE public.tenants,
                   public.tenant_infrastructures,
                   public.outbox,
                   public.inbox RESTART IDENTITY CASCADE;
'

echo "[clean-e2e-data] 6/7 Dropping dynamic shared_db tenant schemas ..."
psql -h "$PGHOST" -p "$(db_port shared_db)" -U "$PGUSER" -d shared_db -v ON_ERROR_STOP=1 -c "
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

echo "[clean-e2e-data] Purging RabbitMQ queues (best-effort) ..."
# Purging the broker BEFORE Mailpit closes the race where in-flight
# notification events re-populate the inbox right after we wipe it.
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
         auth_service_user_created_membership_dlq \
         payment_service_order_created; do
  rabbit_purge "$q"
done

echo "[clean-e2e-data] Purging Mailpit inbox ..."
curl -s -X DELETE "http://localhost:${MAILPIT_DASHBOARD_PORT}/api/v1/messages" -o /dev/null || true

echo "[clean-e2e-data] Done. Platform is clean and ready for the next run."
