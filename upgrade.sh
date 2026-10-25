#!/usr/bin/env bash
set -Eeuo pipefail
umask 077
cd "$(dirname "$0")"
[[ -r secrets/project_name ]] && export COMPOSE_PROJECT_NAME="$(cat secrets/project_name)"
export COMPOSE_PARALLEL_LIMIT=1
ADMIN_PATH="$(cat secrets/admin_path)"; FLYBOARD_PORT="${FLYBOARD_PORT:-8080}"
export ADMIN_PATH FLYBOARD_PORT
mkdir -p backups; chmod 700 backups
stamp="$(date -u +%Y%m%dT%H%M%SZ)"; backup="$PWD/backups/pre-upgrade-$stamp.tar"
docker compose run --rm migrate doctor
./backup.sh "$backup" >/dev/null
old_images="$(docker compose images --format json 2>/dev/null || true)"
# shellcheck disable=SC2154 # rc is assigned at trap execution.
trap 'rc=$?; if ((rc)); then printf "Upgrade failed. Verified rollback backup: %s\nImage inventory: backups/pre-upgrade-%s.images.json\n" "$backup" "$stamp" >&2; printf "%s\n" "$old_images" >"backups/pre-upgrade-$stamp.images.json"; fi; exit $rc' EXIT
docker compose pull --ignore-buildable
docker compose build --pull
docker compose run --rm migrate migrate up
docker compose up -d --remove-orphans panel worker scheduler
for _ in $(seq 1 90); do curl -fsS "http://127.0.0.1:${FLYBOARD_PORT}/health/ready" >/dev/null && ready=1 && break; sleep 2; done
[[ "${ready:-}" == 1 ]]
docker compose run --rm migrate doctor
echo "Upgrade complete; verified backup: $backup"
