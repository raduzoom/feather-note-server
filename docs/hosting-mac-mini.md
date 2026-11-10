# Running Feather Cloud on a Mac mini

How Feather Cloud runs for 1.0.0: free (billing off), one storage quota for every account, in Docker on a Mac mini, reached through a Cloudflare Tunnel. Nothing on the Mac is opened to the internet: `cloudflared` dials out to Cloudflare, which serves the domain over HTTPS.

The website and Feather Cloud use two different domains. Two placeholders stand for them until they exist:
- `CLOUD_DOMAIN`: Feather Cloud's address, on its own domain, for example `cloud.example-sync.app`;
- `WEBSITE_DOMAIN`: the website's address, on another domain, served by Cloudflare Pages.

Step 8 lists every place to replace them.

## 1. Domains

1. Website: in the Cloudflare dashboard, open **Domain Registration › Register Domains** and buy the website's domain.
2. Feather Cloud: buy its domain the same way. If you buy it from another registrar, add it to Cloudflare with **Add a domain** and switch its nameservers to the two Cloudflare shows. The tunnel only works for a domain whose DNS is on Cloudflare.

`CLOUD_DOMAIN` can be the bare domain or a subdomain of it, such as `cloud.<domain>`.

## 2. The tunnel

1. Open **Zero Trust › Networks › Tunnels › Create a tunnel**, choose **Cloudflared** and name it `feather-cloud`.
2. On the install step, choose **Docker** and copy the token: the long string after `--token`. You need it in step 4.
3. Under **Public Hostnames › Add**, set the subdomain and domain to `CLOUD_DOMAIN`, the service type to **HTTP**, and the URL to `server:8420`.
4. Under the hostname's **Additional application settings › HTTP Settings**, leave **No TLS Verify** off. The tunnel talks to the server inside Docker over HTTP; Cloudflare handles HTTPS to the world.

## 3. Email for verification codes

Pick a provider that sends from your own domain, for example [Resend](https://resend.com) or [Postmark](https://postmarkapp.com).

1. Add Feather Cloud's domain as a sending domain in the provider. The codes then come from the domain the app talks to, not the website's.
2. Add the DNS records it shows (SPF, DKIM, and usually a return-path CNAME) in Cloudflare **DNS › Records**. Wait until the provider shows the domain as verified.
3. Create SMTP credentials:
   - **Resend:** host `smtp.resend.com`, port 587, user `resend`, and an API key as the password.
   - **Postmark:** host `smtp.postmarkapp.com`, port 587, with the server token as both user and password.

## 4. The Mac mini

1. Install Docker Desktop. In its settings, turn on **Start Docker Desktop when you sign in**. Under **Resources**, give it at least 2 GB of memory and enough disk for your users' notes.
2. In **System Settings › Energy**, turn on **Prevent automatic sleeping when the display is off** and **Start up automatically after a power failure**.
3. Set **System Settings › Users & Groups › Automatically log in as** to the account that runs Docker, so the stack comes back after a restart.
4. Get the server:

   ```bash
   git clone git@github.com:raduzoom/feather-note-server.git ~/feather-note-server
   ```

5. Create the settings file. In `~/feather-note-server`, run `cp deploy/macmini/.env.example deploy/.env`, then fill in every value:
   - **Passwords:** `POSTGRES_PASSWORD` and `S3_SECRET_KEY`; generate each with `openssl rand -hex 24`.
   - **Server secret:** `FEATHER_SERVER_SECRET`; generate it with `openssl rand -hex 32`.
   - **Quota:** `FEATHER_QUOTA_BYTES` is every account's storage. 1 GB is `1073741824`.
   - **Capacity:** `SEAWEED_VOLUME_MAX` is the storage capacity in GB. Backups keep 14 full copies of the stored files on the same disk (step 6), so keep it below about a fifteenth of the Mac mini's free disk, or move the backups elsewhere with `FEATHER_BACKUP_DIR`.
   - **Email:** the `FEATHER_SMTP_*` values from step 3. Set `FEATHER_SMTP_FROM` to an address on the verified domain.
   - **Tunnel:** `CLOUDFLARE_TUNNEL_TOKEN` from step 2.

   `deploy/.env` holds secrets. It is git-ignored; never copy it anywhere else.

## 5. Start it

```bash
cd ~/feather-note-server && docker compose -f deploy/docker-compose.yml -f deploy/macmini/docker-compose.macmini.yml up -d --build --wait
```

Check it from any computer: `curl https://CLOUD_DOMAIN/v1/server` shows `"billing_enabled":false` and `"default_quota_bytes"` equal to your quota.

Then create an account in the app (step 8) and check that the code email arrives. If it doesn't, look at the server's output for mail errors:

```bash
cd ~/feather-note-server && docker compose -f deploy/docker-compose.yml -f deploy/macmini/docker-compose.macmini.yml logs --tail 50 server
```

## 6. Backups

`deploy/macmini/backup.sh` saves the database and the encrypted files into `~/FeatherCloudBackups/<date>/` and keeps 14 days; older folders are deleted outright, not moved to the Trash. Install the daily 03:30 run:

1. Edit `deploy/macmini/app.feathermark.backup.plist` and replace `SERVER_REPO` with the repo's full path, for example `/Users/you/feather-note-server`.
2. Then run:

   ```bash
   cp ~/feather-note-server/deploy/macmini/app.feathermark.backup.plist ~/Library/LaunchAgents/ && launchctl bootstrap gui/$(id -u) ~/Library/LaunchAgents/app.feathermark.backup.plist
   ```

Run one backup by hand and check its folder has `postgres.dump` and `s3data.tgz`:

```bash
~/feather-note-server/deploy/macmini/backup.sh
```

Copy the backups folder off the Mac mini regularly, to another disk or a cloud drive. Everything in it is account metadata and data encrypted on users' devices.

To restore, run `deploy/macmini/restore.sh ~/FeatherCloudBackups/<date>`. It stops the server, empties the database and the file store, puts the backup's copies in their place, and starts everything again. Anything stored after that backup is gone. `deploy/macmini/backup_test.sh` proves the round trip on a throwaway stack.

## 7. Keeping it running

- **Docker restarts the containers by itself** (`restart: unless-stopped` on every service), so the stack comes back after a restart or power cut once Docker Desktop starts. `deploy/macmini/config_test.sh` checks this.
- **Status:** see what's running with `docker compose -f deploy/docker-compose.yml -f deploy/macmini/docker-compose.macmini.yml ps`.
- **Upgrade:**

  ```bash
  cd ~/feather-note-server && git pull && docker compose -f deploy/docker-compose.yml -f deploy/macmini/docker-compose.macmini.yml up -d --build --wait
  ```

  Database changes apply when the server starts.

## 8. Point the app and website at it

Replace the placeholders, then rebuild:
- **Mac app:** `apple/project.yml`, the `FeatherCloudURL: https://CLOUD_DOMAIN` line. Leave `FeatherCloudBilling: false` for 1.0.0.
- **Email sender:** `FEATHER_SMTP_FROM` in `deploy/.env` on the Mac mini.
- **Website:**
  1. In the website repo, replace `WEBSITE_DOMAIN` in every file under `site/` with the website's domain (canonical URLs, the sitemap, `robots.txt`, `llms.txt`, structured data). Then run `python3 scripts/check_site.py --publish`; it must print `site OK`.
  2. In Cloudflare **Workers & Pages › Create › Pages › Connect to Git**, choose the `feather-note-website` repo, leave the build command empty, set the output directory to `site`, then add `WEBSITE_DOMAIN` under **Custom domains**.
  3. Add the site to [Google Search Console](https://search.google.com/search-console) and [Bing Webmaster Tools](https://www.bing.com/webmasters) (Bing also feeds Copilot and ChatGPT search), verify it with the DNS record each one shows, and submit `https://WEBSITE_DOMAIN/sitemap.xml`.
