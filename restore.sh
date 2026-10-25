#!/usr/bin/env bash
set -Eeuo pipefail
umask 077
cd "$(dirname "$0")"
[[ $# == 1 ]] || { echo "Usage: $0 BACKUP.tar" >&2; exit 2; }
[[ "${FLYBOARD_RESTORE_CONFIRM:-}" == YES ]] || { echo "restore may overwrite data; set FLYBOARD_RESTORE_CONFIRM=YES" >&2; exit 2; }
archive="$(realpath "$1")"; tmp="$(mktemp -d)"; trap 'rm -rf "$tmp"' EXIT
# Reject traversal, absolute paths, links and special files before extraction.
while IFS= read -r name; do
  [[ "$name" != /* && "$name" != .. && "$name" != ../* && "$name" != */../* && "$name" != */.. ]] || { echo 'unsafe archive path' >&2; exit 1; }
done < <(tar -tf "$archive")
tar -tvf "$archive" | awk 'substr($1,1,1) !~ /[-d]/ { bad=1 } END { exit bad }' || { echo 'unsafe archive entry type' >&2; exit 1; }
tar --no-same-owner --no-same-permissions -C "$tmp" -xf "$archive"
( cd "$tmp"; sha256sum -c MANIFEST.sha256 )
[[ -s "$tmp/database.tar" && -d "$tmp/secrets" ]] || { echo 'incomplete backup' >&2; exit 1; }
if [[ -e secrets && "${FLYBOARD_RESTORE_SECRETS_CONFIRM:-}" != REPLACE ]]; then
  echo 'secrets already exist; set FLYBOARD_RESTORE_SECRETS_CONFIRM=REPLACE' >&2; exit 2
fi
[[ -r "$tmp/secrets/project_name" ]] && export COMPOSE_PROJECT_NAME="$(cat "$tmp/secrets/project_name")"
mkdir -p backups
pre="$(./backup.sh "backups/pre-restore-$(date -u +%Y%m%dT%H%M%SZ).tar")"
rm -rf secrets; cp -a "$tmp/secrets" secrets; chmod 700 secrets; chmod 600 secrets/*
docker compose run --rm -e FLYBOARD_RESTORE_CONFIRM=YES -v "$tmp:/backup" migrate restore /backup/database.tar
docker compose run --rm migrate migrate up
docker compose up -d postgres valkey panel worker scheduler
port="${FLYBOARD_PORT:-8080}"; for _ in $(seq 1 90); do curl -fsS "http://127.0.0.1:$port/health/ready" >/dev/null && { echo "restore complete; safety backup: $pre"; exit 0; }; sleep 2; done
echo 'restore completed but readiness failed' >&2; exit 1
