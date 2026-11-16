# Self-hosting Feather Cloud

Feather Cloud is the sync server for FeatherMark. You can run your own: it stores only encrypted data, which it cannot read, and the Mac app talks to it like the hosted service.

## What you need

- A machine with Docker and Docker Compose.
- A domain name pointing at it, for HTTPS. The apps refuse plain HTTP except on `localhost`.
- About 1 GB of RAM, plus disk space for your notes and attachments.

## Start it

1. Get the server: `git clone https://github.com/raduzoom/feather-note-server.git`, then `cd feather-note-server`.
2. Create `deploy/.env` from `deploy/.env.example` and fill in the three secrets; compose refuses to start without them:
   - `POSTGRES_PASSWORD` and `S3_SECRET_KEY`: generate each with `openssl rand -hex 24`;
   - `FEATHER_SERVER_SECRET`: generate it with `openssl rand -hex 32`.

   Also check `FEATHER_QUOTA_BYTES`:
   the storage each account may use (default 10 GB).
3. Start the stack:

   ```bash
   docker compose -f deploy/docker-compose.yml up -d --build --wait
   ```

4. Check it: `curl http://localhost:8420/v1/server` shows `"billing_enabled":false` and your quota.

This runs three containers:
- the server, on port 8420;
- Postgres, which is reachable only inside Docker;
- SeaweedFS for encrypted files, also reachable only inside Docker.

Only the server's port is published, and only on `127.0.0.1`, so nothing is reachable from other machines until you add the reverse proxy below. Keep it that way: the server trusts the proxy to report client addresses.

To look inside the storage while developing, add `-f deploy/docker-compose.debug.yml` to the command above: it publishes SeaweedFS's S3 API on `127.0.0.1:8421` and its web UI on `127.0.0.1:8422`. That web UI has no login and can delete every file, so never use the debug file on a server.

## Put it behind HTTPS

Run a reverse proxy in front of port 8420. With [Caddy](https://caddyserver.com), the whole `Caddyfile` is:

```
notes.example.com {
    reverse_proxy localhost:8420
}
```

Run Caddy on the same machine (it reaches the server at `localhost:8420`). Caddy gets and renews the certificate itself. Then set `FEATHER_TRUST_PROXY=true` in `deploy/.env` and run `up -d` again, so rate limits see your users' real addresses instead of the proxy's.

## Connect the app

In FeatherMark, open Settings › Advanced › Server and enter `https://notes.example.com`. With billing off, the app shows no trial or subscription screens: every account is active, with the quota you set.

## Verification codes

Signing up sends a 6-digit code by email. This server has no email provider yet; it writes each email to its output instead. To read a code:

```bash
docker compose -f deploy/docker-compose.yml logs server | grep "mail to"
```

The request log itself never contains emails, codes, tokens or note data.

## Limits to know

- **One server process.** Rate limits are kept in memory, so run one server container, not several behind a load balancer.
- **Blob size.** Attachments are limited to `FEATHER_MAX_BLOB_BYTES` (100 MB by default).
- **Other storage.** To use another S3-compatible store (AWS S3, Cloudflare R2, MinIO), set `FEATHER_S3_ENDPOINT`, `FEATHER_S3_REGION`, `FEATHER_S3_BUCKET`, `FEATHER_S3_ACCESS_KEY` and `FEATHER_S3_SECRET_KEY` on the server service, and drop the `s3` service.

## Back up

Back up two Docker volumes, together:
- `feather_pgdata`: accounts, devices and the item index;
- `feather_s3data`: the encrypted files.

Notes and attachments in them are encrypted on your devices before upload, so a backup alone doesn't show their contents. But the database also holds every account's email address and the data the server checks passwords against. Anyone with a backup can try to guess passwords offline, and a weak password that falls opens that account's notes. So keep backups readable only by you (`chmod 700` on the folder), and encrypt any copy you store elsewhere, for example on an encrypted disk or in an encrypted archive.

## Upgrade

```bash
git pull && docker compose -f deploy/docker-compose.yml up -d --build --wait
```

Database changes apply when the server starts.

## Deleting data

Deleting an account in the app signs every device out at once. The server removes its notes and files on its next cleanup pass, which runs every 10 minutes.
