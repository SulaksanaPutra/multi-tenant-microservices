#!/usr/bin/env bash
# Microservice Platform — deploy / test launcher (step-by-step wizard).
#
# Usage:
#   ./scripts/up.sh              run the wizard:
#                                1. choose deployment tier
#                                2. deploy
#                                3. run unit tests?   (optional)
#                                4. run e2e tests?    (optional)
#                                5. done
#
# Tiers:
#   lite     one shared postgres container for everything; dedicated tenants are
#            per-tenant databases + roles inside it (DEDICATED_ISOLATION_MODE=same_instance)
#   standard shared postgres for the control plane; dedicated container per tenant (default)
#   premium  one dedicated postgres per service (auth-db/user-db/tenant-db/notification-db/payment-db
#            + data-plane-db for shared_db); dedicated container per tenant
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
ENV_FILE="$ROOT/infrastructure/.env"

# Auto-register local Git hooks
if command -v git >/dev/null 2>&1 && [ -d "$ROOT/.git" ]; then
  git config core.hooksPath "$ROOT/.githooks" || true
fi

DEPLOY_DIRS=(auth-service tenant-service user-service order-service payment-service notification-service)
TEST_DIRS=(auth-service tenant-service user-service order-service payment-service notification-service infra-provisioner)
E2E_MIGRATION_FILTER='TestE2E_TC_E2E_030|TestE2E_TC_E2E_032|TestE2E_TC_E2E_033'

# ---------------------------------------------------------------------------
# Tier helpers
# ---------------------------------------------------------------------------

read_tier() {
  local t="${TIER:-}"
  if [ -z "$t" ] && [ -f "$ENV_FILE" ]; then
    t="$(sed -n 's/^TIER=//p' "$ENV_FILE" | tr -d '[:space:]' | tail -1)"
  fi
  echo "${t:-standard}"
}

set_tier() {
  local tier="$1"
  if [ -f "$ENV_FILE" ]; then
    grep -v '^TIER=' "$ENV_FILE" > "$ENV_FILE.tmp" && mv "$ENV_FILE.tmp" "$ENV_FILE"
  fi
  printf 'TIER=%s\n' "$tier" >> "$ENV_FILE"
}

tier_description() {
  case "$1" in
    lite)     echo "one shared postgres for everything; dedicated = per-tenant DB+role in it" ;;
    standard) echo "shared postgres control plane; dedicated container per tenant (default)" ;;
    premium)  echo "one postgres per service + per-tenant containers (max isolation)" ;;
    *)        echo "unknown tier" ;;
  esac
}

# ---------------------------------------------------------------------------
# Actions
# ---------------------------------------------------------------------------

deploy() {
  local tier="$1"
  local profile_args=""
  case "$tier" in
    lite)
      export DEDICATED_ISOLATION_MODE=same_instance
      export SHARED_DB_HOST=postgres
      export AUTH_DB_HOST=postgres USER_DB_HOST=postgres TENANT_DB_HOST=postgres NOTIFICATION_DB_HOST=postgres PAYMENT_DB_HOST=postgres
      ;;
    standard)
      export DEDICATED_ISOLATION_MODE=container
      export SHARED_DB_HOST=postgres
      export AUTH_DB_HOST=postgres USER_DB_HOST=postgres TENANT_DB_HOST=postgres NOTIFICATION_DB_HOST=postgres PAYMENT_DB_HOST=postgres
      ;;
    premium)
      export DEDICATED_ISOLATION_MODE=container
      export SHARED_DB_HOST=data-plane-db
      export AUTH_DB_HOST=auth-db USER_DB_HOST=user-db TENANT_DB_HOST=tenant-db NOTIFICATION_DB_HOST=notification-db PAYMENT_DB_HOST=payment-db
      profile_args="--profile per-service-db"
      ;;
    *)
      echo "Unknown tier '$tier' (expected lite|standard|premium)." >&2
      exit 1
      ;;
  esac

  export RABBITMQ_URL="${RABBITMQ_URL:-amqp://guest:guest@rabbitmq:5672/}"
  export AUTH_SERVICE_URL="${AUTH_SERVICE_URL:-http://auth-service:8085}"
  export TENANT_SERVICE_URL="${TENANT_SERVICE_URL:-http://tenant-service:8082}"
  export SMTP_HOST="${SMTP_HOST:-mailpit}"

  echo "==> Deploying tier '$tier' (DEDICATED_ISOLATION_MODE=$DEDICATED_ISOLATION_MODE)"
  (cd "$ROOT/infrastructure" && docker compose $profile_args up -d --build)
  for svc in "${DEPLOY_DIRS[@]}"; do
    (cd "$ROOT/$svc" && docker compose up -d --build)
  done
  echo "==> Tier '$tier' deployed."
}

run_unit_tests() {
  echo "==> Running unit tests across all services..."
  for svc in "${TEST_DIRS[@]}"; do
    echo "--- $svc ---"
    (cd "$ROOT/$svc" && go test ./...)
  done
  echo "==> Unit tests passed."
}

run_e2e() {
  local tier="$1"
  local e2e_status=0
  echo "==> Running e2e migration-contract tests (tier='$tier')..."
  echo "    (TC-E2E-030 plan upgrade/downgrade + 032 423 shield + 033 rollback)"
  set +e
  # shellcheck disable=SC2086
  (cd "$ROOT/e2e-tests" && TIER="$tier" go test -p 1 -v -run "$E2E_MIGRATION_FILTER" ./...)
  e2e_status=$?
  set -e

  echo ""
  echo "==> Cleaning up e2e test data..."
  if bash "$ROOT/infrastructure/scripts/clean-e2e-data.sh" --tier "$tier"; then
    echo "==> E2E test data cleaned."
  else
    echo "==> Warning: e2e cleanup failed." >&2
  fi

  if [ "$e2e_status" -ne 0 ]; then
    echo "==> E2E tests FAILED (tier='$tier')." >&2
    return "$e2e_status"
  fi
  echo "==> E2E tests passed (tier='$tier')."
}

# ---------------------------------------------------------------------------
# Wizard helpers
# ---------------------------------------------------------------------------

confirm() {
  local prompt="$1"
  local default="${2:-n}"
  local answer
  while true; do
    read -r -p "$prompt [y/N] " answer
    case "${answer:-$default}" in
      y|Y|yes|YES) return 0 ;;
      n|N|no|NO|"") return 1 ;;
      *) echo "Please answer yes or no." ;;
    esac
  done
}

# ---------------------------------------------------------------------------
# Wizard
# ---------------------------------------------------------------------------

wizard() {
  echo "============================================================"
  echo "  Microservice Platform — Deploy / Test Launcher"
  echo "============================================================"
  echo "  Current tier : $(read_tier)"
  echo ""

  # Step 1 — deployment tier
  echo "Step 1/3 — Choose a deployment tier (press Enter for standard):"
  echo "  1) lite"
  echo "  2) standard"
  echo "  3) premium"

  local tier=""
  while true; do
    read -r -p "Tier: " REPLY
    case "${REPLY}" in
      1|lite)
        tier="lite"
        break
        ;;
      2|standard|"")
        tier="standard"
        break
        ;;
      3|premium)
        tier="premium"
        break
        ;;
      *)
        echo "  -> Invalid choice: $REPLY (pick 1-3 or press Enter for standard)."
        ;;
    esac
  done

  [ -z "${tier:-}" ] && { echo "Aborted."; exit 0; }
  set_tier "$tier"
  echo "  -> $(tier_description "$tier")"

  # Step 2 — deploy
  echo ""
  echo "Step 2/3 — Deploy platform"
  deploy "$tier"

  # Step 3 — tests
  echo ""
  echo "Step 3/3 — Tests"
  if confirm "Run unit tests across all services?"; then
    run_unit_tests
  else
    echo "Skipping unit tests."
  fi

  if confirm "Run e2e migration-contract tests (tier=$tier)?"; then
    run_e2e "$tier"
  else
    echo "Skipping e2e tests."
  fi

  echo ""
  echo "==> Done. Tier '$tier' deployed; tests as selected."
}

wizard