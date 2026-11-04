# Deployment

[简体中文](../deployment.md) | English

FiftyAPI runs as a single instance using SQLite on persistent local disk. Do not run replicas against one database or place it on network storage. No Redis is required.

## Binary

Build from source with Go 1.25+; Node is unnecessary when using the included frontend assets:

```sh
go build -buildvcs=false -o bin/fiftyapi ./cmd/fiftyapi
./scripts/start-local.sh
```

The local helper saves its master key and setup token under `.local/` and reuses them. For explicit configuration or a downloaded package, follow the [release quickstart](release-quickstart.md).

| Variable | Purpose |
| --- | --- |
| `FIFTYAPI_MASTER_KEY` | Optional Base64-encoded 32-byte key; when blank, generate `master.key` beside the database |
| `FIFTYAPI_SETUP_TOKEN` | Optional setup token (at least 32 bytes); when blank, generate `setup-token` beside the database |
| `FIFTYAPI_DB_PATH` | Database path, e.g. `data/fiftyapi.db` |
| `FIFTYAPI_LISTEN` | Listen address, e.g. `127.0.0.1:8080` |
| `FIFTYAPI_COOKIE_SECURE` | Set `true` when serving through HTTPS |
| `FIFTYAPI_PUBLIC_URL` | Public site origin used for invitation links |
| `TZ` | Service timezone, including scheduled tasks |

Keep the original master key separately from the database. Losing it prevents recovery of encrypted upstream credentials and settings. The setup token cannot replace it. Database schema migrations run automatically at startup, under a single-instance lock.

## Docker Compose

From the source checkout:

```sh
cp .env.example .env
# Both key fields may remain blank; unique random values are generated automatically.
docker compose up -d --build
```

When blank, the service generates `/data/master.key` and `/data/setup-token` with mode 0600 and reuses them across restarts and container recreation. Read the initial setup token with `docker compose exec fiftyapi cat /data/setup-token`. Explicit environment values take precedence and must satisfy their format and length requirements; do not use a shared value such as `fiftyapi`. Compose binds `127.0.0.1:8080`, stores data in a named volume, runs with a read-only root filesystem and allows 60 seconds for shutdown. Keep `.env` private and back it up separately. Set `FIFTYAPI_COOKIE_SECURE=true` for public HTTPS access. Compose defaults to `TZ=Asia/Shanghai`; change it if desired.

The provided image is built locally. No public image registry or release download URL is assumed.

## Published images

Once an image is published, set `FIFTYAPI_IMAGE` in `.env` to its actual registry name/tag and run `docker compose pull` followed by `docker compose up -d --no-build`. Alternatively, replace `YOUR_IMAGE:TAG` below with the actual published image:

```sh
docker run -d --name fiftyapi --restart unless-stopped --stop-timeout 60 \
  -p 8080:8080 -v fiftyapi-data:/data dyunwei/fiftyapi:latest
docker exec fiftyapi cat /data/setup-token
```

No key variables are required for a new installation. Open `http://SERVER_LAN_IP:8080` and initialize your administrator with the generated token. Configure the public URL only when needed for invitations, in environment variables or web email settings. The image name above is a placeholder; publishing/pushing the image is a separate step.

Persist the data volume and separately back up the generated master key. Online database archives still exclude it. If a database already exists and its original key is unavailable, startup fails without generating a replacement key. Restore the original key before restoring an existing database. Custom database paths use key files in the same directory as that database.

## HTTPS reverse proxy

Terminate HTTPS at your reverse proxy and forward to the local service. Preserve Host, disable response buffering for streaming and allow sufficient request duration. The invitation public URL can be an HTTP or HTTPS origin without a path, query or fragment, including LAN addresses such as `http://192.168.1.10:8080`. Keep `FIFTYAPI_COOKIE_SECURE=false` for HTTP.

Native Responses WebSocket uses `GET /v1/responses`, with `/responses` also supported. For Nginx, the corresponding location needs Upgrade support:

```nginx
location = /v1/responses {
    proxy_pass http://127.0.0.1:8080;
    proxy_http_version 1.1;
    proxy_set_header Host $host;
    proxy_set_header Upgrade $http_upgrade;
    proxy_set_header Connection "upgrade";
    proxy_read_timeout 3600s;
    proxy_buffering off;
}
```

This path also serves HTTP POST. Configure the remaining paths and your domain/HTTPS separately. Browser Origin checks default to same-origin behavior. Connection context is lost on restart; clients must resend complete history.

## Upgrade and rollback

1. Stop the service and back up the database, any existing WAL/SHM files, and the original master key.
2. Replace the binary or rebuild the container using the same data path and key.
3. Start one instance; migrations apply automatically. The public preview uses schema 9.
4. Verify account/model status and client protocols. Maximum concurrency is configured under **Scheduling & health**; saving connection settings does not change it.

Rollback requires the older program together with its pre-upgrade database backup. Never open a migrated database with an older binary. The public version number starts at 0.1.0; that does not reset the database schema or discard existing data.

Allow at least 60 seconds for graceful shutdown. OAuth token rotation can continue for up to 30 seconds so the new token can be persisted before database closure. Forced termination or power failure cannot guarantee recovery of a remotely rotated token.

## Backups and restoration

For a simple offline backup: stop the service, copy the entire database directory including existing WAL/SHM files, and back up the master key separately. Copying only a live `.db` file is not a complete backup.

The offline tool requires Linux/Unix and Python 3.9+. Stop the service and any import command first:

```sh
python3 scripts/backup-offline.py create --db data/fiftyapi.db --output backups/first-snapshot
python3 scripts/backup-offline.py restore --from backups/first-snapshot --output restored-data
```

Output directories must not already exist. The tool checks service locks and checksums and does not copy the master key. Start the restored database using `FIFTYAPI_DB_PATH=restored-data/fiftyapi.db` and the original key. Do not run the original and restored instances against the same upstream credentials or remote backup objects simultaneously.

Online S3/R2 backups create a consistent SQLite snapshot, including committed WAL data, and a checksum manifest. Archives do not contain the master key. See [backup administration](administration.md#backups) for schedules, storage permissions and restoration steps.

## Diagnostics

`fiftyapi version` works without a master key. `/api/version` returns the version and build commit. Request IDs identify individual calls; request logs do not store full prompts or responses. Runtime logs retain operational summaries for 7 days. Errors before database initialization and runtime-log write failures remain visible in terminal/container logs.

If Antigravity reports `invalid_client` after an environment override, clear `ANTIGRAVITY_OAUTH_CLIENT_SECRET`, recreate/restart the service and generate a new authorization link. Do not reuse a failed callback or disclose environment values, authorization results or tokens.
