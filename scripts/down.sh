#!/usr/bin/env bash
# Tear down the microservice platform (step-by-step wizard).
#
# Usage:
#   ./scripts/down.sh            run the wizard:
#                                1. purge Mailpit messages + RabbitMQ queues?
#                                2. stop all services (keep volumes)
#                                3. wipe volumes for a fresh state?
#                                4. done
#
# The per-service-db profile containers (premium tier) are always cleaned up so a
# tier switch never leaves orphaned database containers behind.
#
# Note: the purge step runs first, while Mailpit/RabbitMQ are still up, because
# both must be running for their purge APIs/CLI to work.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

teardown() {
  local purge="$1"   # "" or "-v"
  echo "==> Stopping services (${purge:-volumes preserved})..."
  for svc in notification-service payment-service order-service user-service tenant-service auth-service; do
    (cd "$ROOT/$svc" && docker compose down $purge)
  done
  (cd "$ROOT/infrastructure" && docker compose --profile per-service-db down $purge)

  # Best-effort cleanup of runtime-provisioned dedicated tenant containers.
  docker ps -aq --filter "name=postgres-tenant-" | xargs -r docker rm -f >/dev/null 2>&1 || true

  if [ "$purge" = "-v" ]; then
    rm -f "$ROOT/.active-tier"
    echo "==> Platform torn down and volumes wiped (fresh state)."
  else
    echo "==> Platform stopped (volumes preserved)."
  fi
}

purge_mailpit() {
  local port="${MAILPIT_DASHBOARD_PORT:-8025}"
  if curl -sf -X DELETE "http://localhost:$port/api/v1/messages" >/dev/null 2>&1; then
    echo "==> Mailpit messages purged."
  else
    echo "==> Mailpit not reachable — skipping."
  fi
}

purge_rabbitmq() {
  if ! docker ps --format '{{.Names}}' | grep -qx 'rabbitmq'; then
    echo "==> RabbitMQ not running — skipping queue purge."
    return 0
  fi
  # shellcheck disable=SC2016
  local queues
  queues="$(docker exec rabbitmq rabbitmqctl -s list_queues name --no-table-headers 2>/dev/null | grep -v '^amq\.gen' || true)"
  if [ -z "$queues" ]; then
    echo "==> No RabbitMQ queues to purge."
    return 0
  fi
  while IFS= read -r queue; do
    [ -z "$queue" ] && continue
    if docker exec rabbitmq rabbitmqctl purge_queue "$queue" >/dev/null 2>&1; then
      echo "==> Purged RabbitMQ queue '$queue'."
    else
      echo "==> Warning: could not purge queue '$queue'." >&2
    fi
  done <<< "$queues"
}

# ---------------------------------------------------------------------------
# Wizard helpers
# ---------------------------------------------------------------------------

confirm() {
  local prompt="$1"
  read -r -p "$prompt [y/N] " answer
  case "$answer" in
    [yY][eE][sS]|[yY]) return 0 ;;
    *) return 1 ;;
  esac
}

# ---------------------------------------------------------------------------
# Wizard
# ---------------------------------------------------------------------------

wizard() {
  echo "============================================================"
  echo "  Microservice Platform — Teardown"
  echo "============================================================"
  echo ""

  # Step 1 — purge mail / queue state (while containers are still up)
  echo "Step 1/2 — Purge mail / queue state"
  if confirm "Purge Mailpit messages and RabbitMQ queues?"; then
    purge_mailpit
    purge_rabbitmq
  else
    echo "Skipping purge."
  fi

  # Step 2 — stop platform
  echo ""
  echo "Step 2/2 — Teardown platform"
  if confirm "Wipe volumes for a fresh state?"; then
    teardown "-v"
  else
    teardown ""
  fi

  echo ""
  echo "==> Done."
}

wizard
