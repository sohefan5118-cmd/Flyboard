#!/usr/bin/env bash
set -Eeuo pipefail
umask 077
cd "$(dirname "$0")"
[[ -r secrets/project_name ]] && export COMPOSE_PROJECT_NAME="$(cat secrets/project_name)"
mkdir -p backups; chmod 700 backups
out="${1:-backups/flyboard-$(date -u +%Y%m%dT%H%M%SZ).tar}"
[[ "$out" != "$PWD"/* && "$out" != /* ]] && out="$PWD/$out"
[[ ! -e "$out" ]] || { echo "refusing to overwrite: $out" >&2; exit 2; }
tmp="$(mktemp -d)"; trap 'rm -rf "$tmp"' EXIT
mkdir "$tmp/payload"
docker compose run --rm -v "$tmp:/backup" migrate backup create /backup/payload/database.tar
cp compose.yaml "$tmp/payload/compose.yaml"
cp -a secrets "$tmp/payload/secrets"
find "$tmp/payload/secrets" -type f -exec chmod 600 {} +
( cd "$tmp/payload"; find . -type f ! -name MANIFEST.sha256 -print0 | sort -z | xargs -0 sha256sum > MANIFEST.sha256 )
tar -C "$tmp/payload" --owner=0 --group=0 -cf "$out" .
chmod 600 "$out"
echo "$out"
