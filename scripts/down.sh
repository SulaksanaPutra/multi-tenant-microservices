#!/usr/bin/env bash
# Tear down the microservice platform. Pass `-v` to also remove volumes (fresh state).
# The per-service-db profile containers (premium tier) are always cleaned up so a
# tier switch never leaves orphaned database containers behind.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

PURGE=""
if [ "${1:-}" = "-v" ]; then
  PURGE="-v"
fi

for svc in notification-service order-service user-service tenant-service auth-service; do
  (cd "$ROOT/$svc" && docker compose down $PURGE)
done
(cd "$ROOT/infrastructure" && docker compose --profile per-service-db down $PURGE)

# Best-effort cleanup of runtime-provisioned dedicated tenant containers.
docker ps -aq --filter "name=postgres-tenant-" | xargs -r docker rm -f >/dev/null 2>&1 || true

echo "==> Platform torn down."
