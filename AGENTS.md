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

Integration tests (Postgres and SeaweedFS, every response checked against `../universal/specs/sync-protocol.openapi.yaml`) run in a container on the compose network, so Postgres stays unpublished:

```bash
docker compose -f deploy/docker-compose.yml -f deploy/docker-compose.test.yml run --rm test
```

Add `go test -tags integration -count=1 -run <Name> ./internal/api/` after `test` to run some. `FEATHER_CONTRACT_PARTIAL=1` (`-e`) skips the "every operation was called" check for partial runs. If Docker crashed mid-run and the build fails with "can't find export data", clear the container's cache: `… run --rm test go clean -cache`.

## Code layout (when adding or changing server behaviour)

- One package per concern under `internal/`: `config`, `store` (pool, embedded migrations), `httpx` (JSON, errors), `logx` (request log), `ratelimit`, `mail`, `auth` (signup, sessions, devices, passwords, deletion), `plans` (state, quota, usage), `blobs`, `items` (push, pull), `janitor`, and `api` (routes and middleware only).
- SQL lives in its domain package. Anything that changes an account's items, blobs or usage locks the account row first (`SELECT … FOR UPDATE`).
- Time comes from an injected `Clock`, never `time.Now()` inside a service, so tests can move past trial ends, code expiry and the 24-hour and 90-day rules.
- Every protocol route is in `api.routes`; `TestEveryOpenAPIOperationIsRouted` keeps it equal to the OpenAPI file. Change `sync-protocol.md` and the OpenAPI file first, then the server.
- Schema changes are new files in `internal/store/migrations/` (`NNNN_name.sql`); never edit a shipped one.
- Never log request bodies, query values other than `limit`, tokens, keys, codes or emails (`TestLogNeverContainsBodiesOrQueries`, `TestScriptedClient`). Internal errors are logged by type only.

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
| Go | `go.mod` directive 1.26 (test dependencies require it); Docker images `golang:1.26` |
| Self-hosting guide | `docs/self-hosting.md` |

## Gotchas

- MinIO no longer publishes Docker images (`minio/minio` is gone from Docker Hub and quay.io as of 2026-11). Local S3 storage is SeaweedFS (`chrislusf/seaweedfs`, pinned tag in the compose file); its S3 credentials come from `S3_ACCESS_KEY` / `S3_SECRET_KEY`, written into an identity file at container start.
- Docker Hub rate-limits anonymous pulls (429). If `docker compose up` fails with 429, wait or run `docker login`.
- Git replaces `.git/index` with a new file on commit, reset and similar commands, and the new file has no `com.dropbox.ignored` flag. Re-run the Dropbox command above after git work.
