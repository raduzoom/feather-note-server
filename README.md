# Feather Cloud server

The sync server for FeatherMark, a free native Markdown note app. Feather Cloud is end-to-end encrypted: the server stores versioned encrypted blobs and an opaque envelope (item id, type, version, size, timestamps) and cannot read titles, notebook names, tags or note text. You can use the hosted service or run your own.

## Running locally

The whole stack (server on port 8420, Postgres, and SeaweedFS for encrypted files), after copying `deploy/.env.example` to `deploy/.env` and setting `FEATHER_SERVER_SECRET` (`openssl rand -hex 32`):

```bash
docker compose -f deploy/docker-compose.yml up -d --build --wait
```

Verification codes appear in `docker compose -f deploy/docker-compose.yml logs server`.

Tests (unit, then integration against Postgres and SeaweedFS in Docker):

```bash
go vet ./... && go test ./...
```

```bash
docker compose -f deploy/docker-compose.yml -f deploy/docker-compose.test.yml run --rm test
```

To run your own server, see [docs/self-hosting.md](docs/self-hosting.md).

## Related repositories

- `feathermark-apple`: the Apple apps, macOS now and iPhone and iPad to come, with their shared Swift packages (MIT)
- `feather-note-universal`: specs, sync protocol, crypto test vectors, designs and plans (CC BY 4.0)

## License

AGPL-3.0, see [LICENSE](LICENSE).

## Trademark

The name FeatherMark stays with the author. Forks must use a different name.
