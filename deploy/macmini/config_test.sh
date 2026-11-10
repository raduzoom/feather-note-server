#!/bin/bash
# Checks the Mac mini stack's merged settings without starting anything: every service comes back by itself after a
# reboot or power cut, and nothing is published on the Mac.
#
#   deploy/macmini/config_test.sh          # from the server repo root; prints "macmini config: OK"
set -euo pipefail
cd "$(dirname "$0")/../.."

CLOUDFLARE_TUNNEL_TOKEN=x FEATHER_SERVER_SECRET=x POSTGRES_PASSWORD=x S3_SECRET_KEY=x docker compose --env-file /dev/null -f deploy/docker-compose.yml -f deploy/macmini/docker-compose.macmini.yml config --format json | python3 -c '
import json, sys
services = json.load(sys.stdin)["services"]
bad = []
for name, s in sorted(services.items()):
    if s.get("restart") not in ("unless-stopped", "always"):
        bad.append(f"{name}: restart is {s.get('"'"'restart'"'"')!r}, so it stays stopped after a reboot")
    if s.get("ports"):
        bad.append(f"{name}: publishes {s['"'"'ports'"'"']}")
if bad:
    sys.exit("macmini config:\n  " + "\n  ".join(bad))
print("macmini config: OK")
'
