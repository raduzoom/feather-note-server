# Feather Cloud server: agent rules

Rules for AI agents in this repo. `CLAUDE.md` is a symlink to this file. The shared rules for all three Feather Note repos are in `../AGENTS.md`; global rules in `~/Dropbox/Configs/claude/AGENTS.md` still apply.

## Ports (when running or adding any server)

- Feather server: 8420 (`-addr` flag or `FEATHER_ADDR` env).
- S3-compatible storage (SeaweedFS, local dev): S3 API 8421, filer web UI 8422.
- Postgres: 5432 inside Docker only, never published to the host.
- Never 3000.

## Build and test (before saying a change works)

From this repo's root:

```bash
go vet ./... && go test ./...
```

Local stack:

```bash
docker compose -f deploy/docker-compose.yml up -d --wait
```

## Dropbox (after cloning, after a build, and after git work)

```bash
for d in .git/index bin; do [ -e "$d" ] && xattr -w com.dropbox.ignored 1 "$d"; done
```

## Specs and plans (when starting or finishing a phase)

Specs, test vectors and the roadmap live in the `universal` repo at `../universal/`. The API contract is `../universal/specs/sync-protocol.openapi.yaml` (from Phase 1). Phase workflow: `../universal/AGENTS.md`.

## Decisions (when naming or versioning anything)

| Decision | Value |
|---|---|
| Go module path | `feathernote/server` |
| Version | `main.version`, default `dev`, set with `-ldflags "-X main.version=…"` |
| License | AGPL-3.0 |
| Logging | Never log request bodies or any title-like field |

## Gotchas

- MinIO no longer publishes Docker images (`minio/minio` is gone from Docker Hub and quay.io as of 2026-11). Local S3 storage is SeaweedFS (`chrislusf/seaweedfs`, pinned tag in the compose file); its S3 credentials come from `S3_ACCESS_KEY` / `S3_SECRET_KEY`, written into an identity file at container start.
- Docker Hub rate-limits anonymous pulls (429). If `docker compose up` fails with 429, wait or run `docker login`.
- Git replaces `.git/index` with a new file on commit, reset and similar commands, and the new file has no `com.dropbox.ignored` flag. Re-run the Dropbox command above after git work.
