#!/bin/bash
# Proves a backup restores (Phase 9 Review Focus 4). Runs a separate compose project, feather-bt, so the dev
# stack's data is never touched: sign up, upload a file, back up, then change the live stack (a second file, a table
# from a newer migration), restore onto it, sign in and download the first file again. Also checks that pruning old
# backups deletes them outright (a headless Mac never empties the Trash).
#
#   deploy/macmini/backup_test.sh          # from the server repo root; prints "backup restores: OK"
set -euo pipefail
cd "$(dirname "$0")/../.."

export COMPOSE_PROJECT_NAME=feather-bt
tmp="$(mktemp -d "${TMPDIR:-/tmp}/feather-bt.XXXXXX")"
cat > "$tmp/override.yml" <<'YAML'
services:
  server:
    ports: !reset []
    environment:
      FEATHER_SERVER_SECRET: "0101010101010101010101010101010101010101010101010101010101010101"
      FEATHER_MAIL: log
  s3:
    ports: !reset []
YAML
export FEATHER_COMPOSE_FILES="-f deploy/docker-compose.yml -f $tmp/override.yml"
read -ra FILES <<< "$FEATHER_COMPOSE_FILES"
compose() { docker compose "${FILES[@]}" "$@"; }
cleanup() { compose down -v > /dev/null 2>&1 || true; }
# FEATHER_BT_KEEP=1 leaves the stack running afterwards, to look at a failure
[ "${FEATHER_BT_KEEP:-}" = 1 ] || trap cleanup EXIT

# curl on the project's network (nothing is published)
api() { docker run --rm --network feather-bt_default -v "$tmp:/w" curlimages/curl:8.10.1 -s "$@"; }

echo "backup test: starting a separate stack"
cleanup
compose up -d --build --wait > /dev/null

email="bt@example.com"
b64() { openssl rand -base64 "$1"; }
auth="$(b64 32)"
cat > "$tmp/signup.json" <<JSON
{"email":"$email","salt":"$(b64 16)","kdf":{"alg":"argon2id13","t":3,"m_kib":262144,"p":1},"auth_key":"$auth",
 "recovery_auth_key":"$(b64 32)","wrapped_master_password":"$(b64 74)","wrapped_master_recovery":"$(b64 74)",
 "library_id":"01JC8ZN0AB1CD2EF3GH4JK5MN6"}
JSON
api -X POST -H 'Content-Type: application/json' --data @/w/signup.json http://server:8420/v1/auth/signup > /dev/null
code=""
for _ in 1 2 3 4 5 6 7 8 9 10; do
	code="$(compose logs server 2>/dev/null | grep "mail to $email" | tail -1 | sed -E 's/.*code is ([0-9]{6}).*/\1/')"
	[ -n "$code" ] && break
	sleep 1
done
[ -n "$code" ] || { echo "backup test: no verification code" >&2; exit 1; }
token="$(api -X POST -H 'Content-Type: application/json' -d "{\"email\":\"$email\",\"code\":\"$code\",\"device_name\":\"bt\"}" \
	http://server:8420/v1/auth/verify | sed -E 's/.*"token":"([^"]+)".*/\1/')"

head -c 5000 /dev/urandom > "$tmp/blob.bin"
sha="$(shasum -a 256 "$tmp/blob.bin" | cut -d' ' -f1)"
api -X PUT -H "Authorization: Bearer $token" -H 'Content-Type: application/octet-stream' --data-binary @/w/blob.bin \
	"http://server:8420/v1/blobs/$sha" > /dev/null

echo "backup test: backing up"
# 15 old backups and a trash command that records any use: pruning must leave 14 folders and never use trash
mkdir -p "$tmp/backups" "$tmp/bin"
for d in 01 02 03 04 05 06 07 08 09 10 11 12 13 14 15; do mkdir "$tmp/backups/2020-01-$d" && echo x > "$tmp/backups/2020-01-$d/postgres.dump"; done
printf '#!/bin/sh\necho "$@" >> "%s/trash-used"\n' "$tmp" > "$tmp/bin/trash" && chmod +x "$tmp/bin/trash"
PATH="$tmp/bin:$PATH" FEATHER_BACKUP_DIR="$tmp/backups" deploy/macmini/backup.sh > /dev/null
backup="$tmp/backups/$(date +%Y-%m-%d)"
[ ! -e "$tmp/trash-used" ] || { echo "backup test: pruning moved backups to the Trash, which frees no disk" >&2; exit 1; }
[ "$(ls -1 "$tmp/backups" | wc -l | tr -d ' ')" = 14 ] || { echo "backup test: pruning kept $(ls -1 "$tmp/backups" | wc -l) folders, not 14" >&2; exit 1; }

echo "backup test: changing the live stack, then restoring onto it"
psql() { compose exec -T postgres sh -c 'psql -U "$POSTGRES_USER" -d "$POSTGRES_DB" -tA' ; }
echo "CREATE TABLE zz_after_backup (id int);" | psql > /dev/null
account="$(echo "SELECT id FROM accounts LIMIT 1;" | psql | tr -d '[:space:]')"
head -c 5000 /dev/urandom > "$tmp/blob2.bin"
sha2="$(shasum -a 256 "$tmp/blob2.bin" | cut -d' ' -f1)"
api -X PUT -H "Authorization: Bearer $token" -H 'Content-Type: application/octet-stream' --data-binary @/w/blob2.bin \
	"http://server:8420/v1/blobs/$sha2" > /dev/null
[ "$(api -o /dev/null -w '%{http_code}' "http://s3:8888/buckets/feather/$account/$sha2")" = 200 ] || { echo "backup test: second file not stored" >&2; exit 1; }
deploy/macmini/restore.sh "$backup" > /dev/null

[ -z "$(echo "SELECT to_regclass('public.zz_after_backup');" | psql | tr -d '[:space:]')" ] || { echo "backup test: a table created after the backup survived the restore" >&2; exit 1; }
[ "$(api -o /dev/null -w '%{http_code}' "http://s3:8888/buckets/feather/$account/$sha2")" = 404 ] || { echo "backup test: a file stored after the backup survived the restore" >&2; exit 1; }

token2="$(api -X POST -H 'Content-Type: application/json' -d "{\"email\":\"$email\",\"auth_key\":\"$auth\",\"device_name\":\"bt2\"}" \
	http://server:8420/v1/auth/login | sed -E 's/.*"token":"([^"]+)".*/\1/')"
api -H "Authorization: Bearer $token2" -o /w/back.bin "http://server:8420/v1/blobs/$sha"
[ "$(shasum -a 256 "$tmp/back.bin" | cut -d' ' -f1)" = "$sha" ] || { echo "backup test: the file didn't come back" >&2; exit 1; }
echo "backup restores: OK"
