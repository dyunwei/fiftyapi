# Deployment

[简体中文](../deployment.md) | English

FiftyAPI runs as a single instance using SQLite on persistent local disk. Do not run replicas against one database or place it on network storage. No Redis is required.

## Binary

Build from source with Go 1.25+; Go 1.26.10 is recommended, matching CI and Docker builds. The minimum language version in `go.mod` remains 1.25.0. Node is unnecessary when using the included frontend assets. Rocky Linux 9.8 can use its system Go toolchain; the pure Go SQLite driver needs no SQLite development libraries. Race tests require GCC, and offline backups require Python 3.9+.

```sh
go build -buildvcs=false -o bin/fiftyapi ./cmd/fiftyapi
./scripts/start-local.sh
```

Use `./scripts/build-release.sh` to create a release package (requires Node 24+). It builds with `CGO_ENABLED=0` and defaults to `GOAMD64=v1` so distribution toolchains defaulting to v2 do not raise the x86-64 CPU requirements. Override `GOAMD64` explicitly when needed; ordinary `go build` keeps the system defaults. Rebuild binaries after upgrading Go to use the new runtime and standard library.

On Rocky Linux with SELinux enabled, keep the default Compose named volume, or use `./data:/data:Z` for a dedicated bind-mounted data directory. The entrypoint prepares data directory ownership automatically before running the service as UID/GID 10001 (an explicit non-root `--user` must already have write access). Loopback-only deployments need no external port opened; configure firewalld for the actual public entry point when exposing the service.

The local helper saves its master key and setup token under `.local/` and reuses them. For explicit configuration or a downloaded package, follow the [release quickstart](release-quickstart.md).

| Variable | Purpose |
| --- | --- |
| `FIFTYAPI_MASTER_KEY` | Optional Base64-encoded 32-byte key; when blank, generate `master.key` beside the database |
| `FIFTYAPI_SETUP_TOKEN` | Optional setup token (at least 8 bytes); when blank, generate `setup-token` beside the database |
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

The command above builds from source. To use the published Docker Hub image, follow the next section.

## Published images

The published image is [`dyunwei/fiftyapi` on Docker Hub](https://hub.docker.com/r/dyunwei/fiftyapi). With the repository's Compose configuration, set `FIFTYAPI_IMAGE=dyunwei/fiftyapi` in `.env`, then run `docker compose pull` followed by `docker compose up -d --no-build`. Without a source checkout, run the image directly:

```sh
mkdir -p data
docker run -d \
  --name fiftyapi \
  --restart unless-stopped \
  --stop-timeout 60 \
  -p 28080:8080 \
  -e TZ=Asia/Shanghai \
  -v "$(pwd)/data:/data" \
  dyunwei/fiftyapi
docker exec fiftyapi cat /data/setup-token
```

No key variables are required for a new installation. Open `http://SERVER_LAN_IP:28080` and initialize your administrator with the generated token. Configure the public URL only when needed for invitations, in environment variables or web email settings. The host port is 28080; the container continues to listen on port 8080.

The command stores data in the host `./data` directory. Keep this directory and separately back up its generated `master.key`. Online database archives still exclude it. If a database already exists and its original key is unavailable, startup fails without generating a replacement key. Restore the original key before restoring an existing database. Custom database paths use key files in the same directory as that database.

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

The Docker image now includes `fiftyapi restore`; no Python or manual extraction is required for online backup archives. From the Compose project directory:

```sh
docker compose stop fiftyapi
docker compose run --rm --no-deps --user 0:0 \
  --entrypoint /app/fiftyapi \
  -v "$PWD/backup.tar.gz:/backup.tar.gz:ro" \
  fiftyapi restore --from /backup.tar.gz --output /data/restored
```

The maintenance command uses explicit `--user 0:0` to read private mounted files. Normal startup fixes data-volume ownership and runs as app. It reads the original key from Compose environment or the current database directory's `master.key`; `--key-file` accepts an explicitly mounted original key (0600 permissions). It never generates a replacement key.

Restoration verifies SHA-256 checksums, SQLite integrity and the original key, then runs supported migrations on a staging copy. Newer database schemas are rejected. Version metadata is included in new archives; existing format 1 archives remain supported using the database's schema version. Early databases without a master-key verification record are rejected. Unpacked data is limited to 32 GiB; allow additional disk space for migration. Existing output paths are rejected. Interrupted operations may leave staging or incomplete output directories; inspect and remove them before retrying into a new directory.

After success, set `FIFTYAPI_DB_PATH=/data/restored/fiftyapi.db` in `.env`, then run `docker compose up -d --force-recreate fiftyapi`. The new directory includes a private copy of the supplied original master key. Check login, API keys, budgets, models, backup schedules and upstream authorization; OAuth tokens may have rotated since the snapshot. Keep the original instance stopped. To roll back, stop the restored instance and switch the database path back; subsequent writes are not merged. For binary deployments, use `fiftyapi restore --from backup.tar.gz --output restored-data --key-file data/master.key`.

## Diagnostics

`fiftyapi version` works without a master key. `/api/version` returns the version and build commit. Request IDs identify individual calls; request logs do not store full prompts or responses. Runtime logs retain operational summaries for 7 days. Errors before database initialization and runtime-log write failures remain visible in terminal/container logs.

If Antigravity reports `invalid_client` after an environment override, clear `ANTIGRAVITY_OAUTH_CLIENT_SECRET`, recreate/restart the service and generate a new authorization link. Do not reuse a failed callback or disclose environment values, authorization results or tokens.
