#!/usr/bin/env bash
# Destructive only to its random Compose project and temporary source copy.
set -Eeuo pipefail
root="$(cd "$(dirname "$0")/.." && pwd)"; tmp="$(mktemp -d)"; project="flyboard-life-$RANDOM-$$"
cleanup(){ (cd "$tmp/src" 2>/dev/null && COMPOSE_PROJECT_NAME="$project" FLYBOARD_PURGE_CONFIRM=PURGE ./uninstall.sh --purge) >/dev/null 2>&1 || true; rm -rf "$tmp"; }
trap cleanup EXIT
cp -a "$root" "$tmp/src"; cd "$tmp/src"
export COMPOSE_PROJECT_NAME="$project" FLYBOARD_PORT="${FLYBOARD_LIFECYCLE_PORT:-18080}"
./install.sh >install.out
[[ "$(stat -c %a secrets/master_key)" == 600 ]]
url="http://127.0.0.1:$FLYBOARD_PORT"; curl -fsS "$url/health/ready" | grep -q ready
email="$(sed -n 's/^Admin email: //p' install.out)"; password="$(sed -n 's/^One-time password: //p' install.out)"
curl -fsS -c cookies -H 'Content-Type: application/json' --data "{\"Email\":\"$email\",\"Password\":\"$password\"}" "$url/login" | grep -q csrf_token
mkdir -p backups
docker compose run --rm -v "$PWD/backups:/backup" migrate backup create /backup/lifecycle.tar
docker compose run --rm -v "$PWD/backups:/backup" migrate backup verify /backup/lifecycle.tar
# Deterministic mutation and restoration proof using a durable audit sentinel.
docker compose exec -T postgres sh -c 'PGPASSWORD="$POSTGRES_PASSWORD" psql -U flyboard -d flyboard' >/dev/null <<'SQL'
insert into audit_logs(action,detail) values ('lifecycle-sentinel','{}');
SQL
docker compose run --rm -e FLYBOARD_RESTORE_CONFIRM=YES -v "$PWD/backups:/backup" migrate restore /backup/lifecycle.tar
sentinel="$(docker compose exec -T postgres sh -c 'PGPASSWORD="$POSTGRES_PASSWORD" psql -At -U flyboard -d flyboard' <<'SQL'
select 1 from audit_logs where action='lifecycle-sentinel';
SQL
)"
[[ "$sentinel" != 1 ]] || { echo 'restore retained post-backup mutation' >&2; exit 1; }
./upgrade.sh
./uninstall.sh
./install.sh >reinstall.out
curl -fsS "$url/health/ready" >/dev/null
FLYBOARD_PURGE_CONFIRM=PURGE ./uninstall.sh --purge
if docker volume ls --format '{{.Name}}' | grep -q "${project}_pgdata"; then echo 'purge retained pgdata' >&2; exit 1; fi
trap - EXIT; rm -rf "$tmp"
echo "lifecycle passed: $project"
