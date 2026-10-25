#!/usr/bin/env bash
set -Eeuo pipefail
cd "$(dirname "$0")"
[[ -r secrets/project_name ]] && export COMPOSE_PROJECT_NAME="$(cat secrets/project_name)"
COMPOSE_PROJECT_NAME="${COMPOSE_PROJECT_NAME:-$(basename "$PWD")}"; export COMPOSE_PROJECT_NAME
ADMIN_PATH="$(cat secrets/admin_path 2>/dev/null || true)"
export ADMIN_PATH
case "${1:-}" in
  ""|--keep-data) docker compose down --remove-orphans; echo "Services removed; data and secrets retained." ;;
  --purge)
    if [[ "${FLYBOARD_PURGE_CONFIRM:-}" != "PURGE" ]]; then
      [[ -t 0 ]] || { echo "Non-interactive purge requires FLYBOARD_PURGE_CONFIRM=PURGE" >&2; exit 2; }
      read -r -p "This deletes all Flyboard data. Type PURGE: " answer
      [[ "$answer" == PURGE ]] || { echo "Purge cancelled." >&2; exit 1; }
      read -r -p "Type the Compose project name ($COMPOSE_PROJECT_NAME) to confirm: " project
      [[ "$project" == "$COMPOSE_PROJECT_NAME" ]] || { echo "Purge cancelled." >&2; exit 1; }
    fi
    docker compose down -v --remove-orphans
    if command -v trash >/dev/null; then trash secrets; else mv secrets "secrets.removed.$(date +%s)"; echo "Secrets moved aside for recoverability."; fi
    ;;
  *) echo "Usage: $0 [--keep-data|--purge]" >&2; exit 2 ;;
esac
