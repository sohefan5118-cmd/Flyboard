#!/usr/bin/env bash
set -Eeuo pipefail
root="$(cd "$(dirname "$0")/.." && pwd)"; tmp="$(mktemp -d)"; trap 'rm -rf "$tmp"' EXIT
cp -a "$root" "$tmp/src"; mkdir "$tmp/bin"; log="$tmp/docker.log"
cat >"$tmp/bin/docker" <<'MOCK'
#!/usr/bin/env bash
echo "$*" >>"$MOCK_LOG"
case "$*" in
  'compose version') exit 0;;
  *'backup create '*) out="${*: -1}"; out="${out#/backup/}"; host="$MOCK_BACKUP"; prev=''; for arg in "$@"; do if [[ "$prev" == -v && "$arg" == *:/backup ]]; then host="${arg%:/backup}"; fi; prev="$arg"; done; mkdir -p "$host/$(dirname "$out")"; printf dump >"$host/$out";;
  *'backup verify '*) exit 0;;
  *'bootstrap-token'*) echo mock-token;;
esac
MOCK
cat >"$tmp/bin/curl" <<'MOCK'
#!/usr/bin/env bash
case "$*" in *health/ready*) echo ready;; *) :;; esac
MOCK
chmod +x "$tmp/bin/"*
export PATH="$tmp/bin:$PATH" MOCK_LOG="$log" MOCK_BACKUP="$tmp/src/backups"
cd "$tmp/src"; FLYBOARD_ADMIN_PASSWORD=test-password ./install.sh >install.out
[[ -s secrets/project_name && $(stat -c %a secrets/master_key) == 600 ]]
./install.sh >reinstall.out; ! grep -q 'One-time password:' reinstall.out
archive="$(./backup.sh backups/mock-full.tar)"
tar -tf "$archive" | grep -q './MANIFEST.sha256'
tar -tf "$archive" | grep -q './secrets/master_key'
verify="$tmp/verify"; mkdir "$verify"; tar -C "$verify" -xf "$archive"; (cd "$verify" && sha256sum -c MANIFEST.sha256 >/dev/null)
if FLYBOARD_RESTORE_CONFIRM=YES ./restore.sh "$archive" >/dev/null 2>&1; then echo 'restore overwrite guard failed' >&2; exit 1; fi
./uninstall.sh
! grep -q -- 'down -v' "$log"
grep -q 'compose up -d panel worker scheduler' "$log"
! grep -q -- '--build' "$log"
echo 'mock lifecycle passed'
