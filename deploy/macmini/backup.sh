#!/bin/bash
# Backs up Feather Cloud: the Postgres database (pg_dump, custom format) and the encrypted files (the SeaweedFS
# volume, as a tarball). Everything in both is encrypted on users' devices or is account metadata.
#
#   deploy/macmini/backup.sh            # from the server repo root, or let launchd run it daily
#
# Writes $FEATHER_BACKUP_DIR/YYYY-MM-DD/ (default ~/FeatherCloudBackups) and keeps the newest 14 days.
# Settings: FEATHER_COMPOSE_FILES (default: the Mac mini stack), COMPOSE_PROJECT_NAME (default: feather).
set -euo pipefail

cd "$(dirname "$0")/../.."
read -ra FILES <<< "${FEATHER_COMPOSE_FILES:--f deploy/docker-compose.yml -f deploy/macmini/docker-compose.macmini.yml}"
compose() { docker compose "${FILES[@]}" "$@"; }
project="${COMPOSE_PROJECT_NAME:-feather}"
base="${FEATHER_BACKUP_DIR:-$HOME/FeatherCloudBackups}"
dir="$base/$(date +%Y-%m-%d)"
mkdir -p "$dir"

echo "backup: database"
compose exec -T postgres sh -c 'pg_dump -U "$POSTGRES_USER" -d "$POSTGRES_DB" -Fc' > "$dir/postgres.dump.partial"
mv "$dir/postgres.dump.partial" "$dir/postgres.dump"

echo "backup: files"
# a few seconds' pause keeps the volume consistent while it is copied
compose pause s3 > /dev/null
trap 'compose unpause s3 > /dev/null 2>&1 || true' EXIT
docker run --rm -v "${project}_s3data:/data:ro" -v "$dir:/backup" alpine:3.20 tar czf /backup/s3data.tgz.partial -C /data .
compose unpause s3 > /dev/null
trap - EXIT
mv "$dir/s3data.tgz.partial" "$dir/s3data.tgz"
# Docker reports a paused container unhealthy for a few seconds after unpausing, and `up --wait` (an upgrade or a
# restore) fails on that; finish only once storage is healthy again
s3="$(compose ps -q s3)"
for _ in $(seq 1 60); do
	[ "$(docker inspect -f '{{.State.Health.Status}}' "$s3")" = healthy ] && break
	sleep 1
done

[ -s "$dir/postgres.dump" ] && [ -s "$dir/s3data.tgz" ] || { echo "backup: empty output in $dir" >&2; exit 1; }

# keep the newest 14 dated folders; only folders named YYYY-MM-DD are ever removed. They are deleted outright, not
# moved to the Trash: nobody empties the Trash on a headless Mac, so the disk would fill.
keep=14
ls -1 "$base" | grep -E '^[0-9]{4}-[0-9]{2}-[0-9]{2}$' | sort -r | tail -n +$((keep + 1)) | while read -r d; do
	rm -r -- "${base:?}/$d"
done

echo "backup: done, $dir"
