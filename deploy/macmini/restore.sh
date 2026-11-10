#!/bin/bash
# Restores a backup made by backup.sh into this stack, replacing its database and files.
#
#   deploy/macmini/restore.sh ~/FeatherCloudBackups/2026-11-10
#
# Stops the server (and the tunnel) first, so nothing writes while restoring, then starts the stack again.
# Settings: FEATHER_COMPOSE_FILES, COMPOSE_PROJECT_NAME, as backup.sh.
set -euo pipefail

src="${1:?usage: restore.sh <backup folder>}"
[ -s "$src/postgres.dump" ] && [ -s "$src/s3data.tgz" ] || { echo "restore: $src has no postgres.dump and s3data.tgz" >&2; exit 1; }
src="$(cd "$src" && pwd)"

cd "$(dirname "$0")/../.."
read -ra FILES <<< "${FEATHER_COMPOSE_FILES:--f deploy/docker-compose.yml -f deploy/macmini/docker-compose.macmini.yml}"
compose() { docker compose "${FILES[@]}" "$@"; }
project="${COMPOSE_PROJECT_NAME:-feather}"

echo "restore: stopping the server"
compose stop server cloudflared > /dev/null 2>&1 || compose stop server > /dev/null
compose up -d --wait postgres s3 > /dev/null

echo "restore: database"
# an empty schema first: --clean alone would keep tables from migrations newer than the backup
compose exec -T postgres sh -c 'PGOPTIONS=--client-min-messages=warning psql -q -v ON_ERROR_STOP=1 -U "$POSTGRES_USER" -d "$POSTGRES_DB" -c "DROP SCHEMA public CASCADE; CREATE SCHEMA public;"'
compose exec -T postgres sh -c 'pg_restore -U "$POSTGRES_USER" -d "$POSTGRES_DB" --no-owner --exit-on-error' < "$src/postgres.dump"

echo "restore: files"
# an empty volume first: files written after the backup (volumes, the filer's log) would otherwise survive and replay
compose stop s3 > /dev/null
docker run --rm -v "${project}_s3data:/data" -v "$src:/backup:ro" alpine:3.20 sh -c 'find /data -mindepth 1 -maxdepth 1 -exec rm -rf {} + && tar xzf /backup/s3data.tgz -C /data'

echo "restore: starting"
compose up -d --wait > /dev/null
echo "restore: done"
