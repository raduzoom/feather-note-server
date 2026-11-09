# Feather Cloud server

The sync server for Feather Note, a free native Markdown note app. Feather Cloud is end-to-end encrypted: the server stores versioned encrypted blobs and an opaque envelope (item id, type, version, size, timestamps) and cannot read titles, notebook names, tags or note text. You can use the hosted service or run your own.

## Running locally

Postgres and S3-compatible storage (SeaweedFS):

```bash
docker compose -f deploy/docker-compose.yml up -d --wait
```

The server (port 8420):

```bash
go run ./cmd/feather-server
```

Tests:

```bash
go vet ./... && go test ./...
```

## Related repositories

- `feather-note-mac`: the macOS app (MIT)
- `feather-note-universal`: specs, sync protocol, crypto test vectors, designs and plans (CC BY 4.0)

## License

AGPL-3.0, see [LICENSE](LICENSE).

## Trademark

The name Feather Note stays with the author. Forks must use a different name.
