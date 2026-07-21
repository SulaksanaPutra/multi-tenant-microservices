#!/usr/bin/env bash
# Deploy the microservice platform at a chosen isolation tier.
#
#   lite     -> one shared postgres container for everything; per-tenant data-plane
#               databases live inside it (DEDICATED_ISOLATION_MODE=same_instance)
#   standard -> shared postgres for the control plane; dedicated container per tenant
#               (DEDICATED_ISOLATION_MODE=container, all services -> postgres)
#   premium  -> dedicated postgres per service (auth-db/user-db/tenant-db/notification-db
#               + data-plane-db for shared_db); dedicated container per tenant
#
# The tier is read from infrastructure/.env (TIER=...). If it is missing or
# invalid the script prompts once, writes the choice, and proceeds.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
ENV_FILE="$ROOT/infrastructure/.env"

set_tier() {
  local tier="$1"
  if [ -f "$ENV_FILE" ]; then
    grep -v '^TIER=' "$ENV_FILE" > "$ENV_FILE.tmp" && mv "$ENV_FILE.tmp" "$ENV_FILE"
  fi
  printf 'TIER=%s\n' "$tier" >> "$ENV_FILE"
}

TIER="${TIER:-}"
if [ -z "$TIER" ] && [ -f "$ENV_FILE" ]; then
  TIER="$(sed -n 's/^TIER=//p' "$ENV_FILE" | tr -d '[:space:]' | tail -1)"
fi

if [ -z "$TIER" ]; then
  echo "No deployment tier configured. Choose one:"
  PS3="Tier (default: standard): "
  select TIER in lite standard premium; do
    if [ -n "$TIER" ]; then break; fi
  done
  set_tier "$TIER"
  echo "Saved TIER=$TIER to $ENV_FILE"
fi

case "$TIER" in
  lite)
    export DEDICATED_ISOLATION_MODE=same_instance
    export SHARED_DB_HOST=postgres
    export AUTH_DB_HOST=postgres USER_DB_HOST=postgres TENANT_DB_HOST=postgres NOTIFICATION_DB_HOST=postgres
    ;;
  standard)
    export DEDICATED_ISOLATION_MODE=container
    export SHARED_DB_HOST=postgres
    export AUTH_DB_HOST=postgres USER_DB_HOST=postgres TENANT_DB_HOST=postgres NOTIFICATION_DB_HOST=postgres
    ;;
  premium)
    export DEDICATED_ISOLATION_MODE=container
    export SHARED_DB_HOST=data-plane-db
    export AUTH_DB_HOST=auth-db USER_DB_HOST=user-db TENANT_DB_HOST=tenant-db NOTIFICATION_DB_HOST=notification-db
    ;;
  *)
    echo "Unknown tier '$TIER' (expected lite|standard|premium). Edit TIER= in $ENV_FILE and re-run." >&2
    exit 1
    ;;
esac

PROFILE_ARGS=""
if [ "$TIER" = "premium" ]; then
  PROFILE_ARGS="--profile per-service-db"
fi

echo "==> Deploying tier '$TIER' (DEDICATED_ISOLATION_MODE=$DEDICATED_ISOLATION_MODE)"

(cd "$ROOT/infrastructure" && docker compose $PROFILE_ARGS up -d --build)
(cd "$ROOT/auth-service" && docker compose up -d --build)
(cd "$ROOT/tenant-service" && docker compose up -d --build)
(cd "$ROOT/user-service" && docker compose up -d --build)
(cd "$ROOT/order-service" && docker compose up -d --build)
(cd "$ROOT/notification-service" && docker compose up -d --build)

echo "==> Tier '$TIER' deployed."
