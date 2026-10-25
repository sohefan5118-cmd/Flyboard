#!/usr/bin/env bash
set -Eeuo pipefail
umask 077
cd "$(dirname "$0")"
command -v docker >/dev/null || { echo "docker is required" >&2; exit 1; }
docker compose version >/dev/null
mkdir -p secrets backups
chmod 700 secrets backups
if [[ ! -s secrets/project_name ]]; then printf 'flyboard-%s\n' "$(openssl rand -hex 6)" >secrets/project_name; fi
export COMPOSE_PROJECT_NAME="$(cat secrets/project_name)" COMPOSE_PARALLEL_LIMIT=1
secret() { local file="secrets/$1"; shift; if [[ ! -s "$file" ]]; then openssl rand "$@" | tr -d '\n' >"$file"; fi; chmod 600 "$file"; }
secret db_password -hex 32
secret valkey_password -hex 32
secret admin_path -hex 20
secret master_key -base64 32
if [[ ! -s secrets/database_url ]]; then printf 'postgres://flyboard:%s@postgres:5432/flyboard?sslmode=disable' "$(cat secrets/db_password)" >secrets/database_url; fi
if [[ ! -s secrets/valkey_url ]]; then printf 'redis://:%s@valkey:6379/0' "$(cat secrets/valkey_password)" >secrets/valkey_url; fi
chmod 600 secrets/*
ADMIN_PATH="$(cat secrets/admin_path)"
FLYBOARD_PORT="${FLYBOARD_PORT:-7001}"
FLYBOARD_BIND="${FLYBOARD_BIND:-0.0.0.0}"
export ADMIN_PATH FLYBOARD_PORT FLYBOARD_BIND
docker compose up -d postgres valkey
docker compose run --rm migrate migrate up

docker compose up -d panel worker scheduler
for _ in $(seq 1 90); do curl -fsS "http://127.0.0.1:${FLYBOARD_PORT}/health/ready" >/dev/null && ready=1 && break; sleep 2; done
[[ "${ready:-}" == 1 ]] || { docker compose ps >&2; echo "Flyboard failed readiness" >&2; exit 1; }

# Bootstrap exactly once. Credentials exist only in memory and this one terminal output.
if [[ ! -e secrets/bootstrap.complete ]]; then
  token="$(docker compose run --rm migrate bootstrap-token | tail -n 1)"
  email="${FLYBOARD_ADMIN_EMAIL:-admin@localhost.invalid}"
  password="${FLYBOARD_ADMIN_PASSWORD:-$(openssl rand -base64 24 | tr -d '\n')}"
  payload="$(printf '{\"Token\":\"%s\",\"Email\":\"%s\",\"Password\":\"%s\"}' "$token" "$email" "$password")"
  curl -fsS -H 'Content-Type: application/json' --data "$payload" "http://127.0.0.1:${FLYBOARD_PORT}/bootstrap" >/dev/null
  : >secrets/bootstrap.complete; chmod 600 secrets/bootstrap.complete
  public_host="${FLYBOARD_PUBLIC_HOST:-SERVER_IP}"
  printf 'Flyboard is ready.\nAdmin URL: http://%s:%s/%s/flyboard\nAdmin email: %s\nOne-time password: %s\nSave these credentials now; the password is shown only once.\n' "$public_host" "$FLYBOARD_PORT" "$ADMIN_PATH" "$email" "$password"
else
  printf 'Flyboard is ready (existing installation; credentials were not reissued).\n'
fi
