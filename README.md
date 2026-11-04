# FiftyAPI

English | [简体中文](README.zh-CN.md)

An AI upstream account and dollar-budget gateway for small teams. One Go service, an embedded Vue 3 interface and SQLite, with no Redis. “Fifty” describes the intended team size, not a hard limit of 50 users.

Current version: **0.1.0**, the first public preview.

- Simplified Chinese and English throughout the console, setup, sign-in, invitations and account contributions. Choose a language in the top-right corner; browser preferences set the initial language and your selection is saved locally.
- Claude Code, OpenAI Codex and Antigravity OAuth authorization, reauthorization, token refresh and model catalog sync.
- API key connections to Sub2Api, FiftyAPI, New API and compatible upstreams, with native Chat Completions, Messages and Responses, discovery and aliases.
- Initial administrator setup, email/name/password self-registration with zero initial budget, and email invitations.
- Per-user dollar budgets across all keys and providers, with 5-hour and 7-day windows.
- Personal request logs; administrator pricing, settlement exceptions, audit and runtime logs.
- Responsive interface, single-binary or Docker deployment, and S3 / Cloudflare R2 backups.

![Admin console](docs/screenshots/admin-dashboard-en.png)

## Install with Docker

The published image is available on [Docker Hub](https://hub.docker.com/r/dyunwei/fiftyapi). Start it without cloning the repository or installing Go/Node:

```sh
docker run -d --name fiftyapi --restart unless-stopped --stop-timeout 60 \
  -p 28080:8080 -v fiftyapi-data:/data dyunwei/fiftyapi
```

Open http://localhost:28080, or `http://SERVER_IP:28080` from another machine. Once the container has started, read the setup token:

```sh
docker exec fiftyapi cat /data/setup-token
```

Enter that token in the setup page and create your administrator account. No environment variables are required for a new installation: the service generates a random master key and setup token, saves them in the `fiftyapi-data` volume and reuses them across container recreation. Keep this volume and separately back up `/data/master.key`; database backup archives do not include the master key.

The left side of `28080:8080` is the host port; change `28080` to use a different port. LAN HTTP is supported. Set an HTTP or HTTPS public URL in email settings when sending invitations, for example `http://192.168.1.10:28080`.

For Compose, HTTPS, upgrades and restoration, see the [deployment guide](docs/en/deployment.md).

## Build and run from source

Requires Go 1.25+; Go 1.26.10 is recommended and used by CI and Docker builds. Built frontend assets are included, so building the server does not require Node. Frontend development requires Node 24+.

```sh
go build -buildvcs=false -o bin/fiftyapi ./cmd/fiftyapi
./scripts/start-local.sh
```

Open http://127.0.0.1:8080. Read the setup token from `.local/setup-token` on the host and enter it during administrator setup. Later starts reuse the database and master key.

After signing in: add upstream accounts → authorize → verify and sync → confirm model prices → allocate member budgets → let members create API keys. Sign-in passwords must be 8–72 bytes.

See the [English documentation](docs/en/README.md), [deployment guide](docs/en/deployment.md), [client configuration](docs/en/clients.md) and [administrator guide](docs/en/administration.md).

## Account contributions

Administrators can create provider-specific contribution links under **Upstream accounts → Contribution links**. Links default to 24 hours and can be disabled early. Multiple account owners can use the same link without signing in, importing up to 20 authorization JSON files totaling 1 MiB per submission.

The public page explains where to find Codex and Claude authorization files on Windows, macOS and Linux, including Keychain and custom-directory cases. Submitted credentials are encrypted and accounts start disabled. Administrators must configure, enable and verify them. Import validates credential format, not actual model access. Full links are displayed only when created.

## Protocols and verification limits

Use the protocol declared by the model catalog. Claude and Antigravity use Messages; Codex OAuth uses Responses. Chat Completions is forwarded only to API upstreams that explicitly support Chat. A model can declare several protocols with one ID and shared prices; standard protocols are not automatically converted.

Real Antigravity tests cover token refresh, discovery, SSE and tool round trips for `gemini-3.5-flash-lite`. Imported OpenAI credentials have passed discovery, text, streaming tool round trips, cancellation and budget accounting. Native Codex short-prompt and Responses WebSocket tests passed. Real Claude authorization, OpenAI authorization-code exchange and refresh rotation, compact with the newer client identity, and long-running native workflows remain unverified. A model appearing in a catalog does not prove permission to call it.

Responses HTTP/SSE, native Responses WebSocket and the compact endpoint are implemented; compact availability depends on the account and upstream and remains unverified with the newer identity. Images/audio generation, payments, paid plans and complex grouping are outside scope. See [compatibility](docs/en/compatibility.md).

## Development

```sh
make test                   # Go race, vet, frontend unit tests and source checks
make test-load              # 50-user simulated load; no real providers
make build                  # npm ci, frontend build and Go binary
make test-ui                # requires Playwright Chromium
./scripts/build-release.sh  # local architecture release files in dist/
```

Install the browser once with `cd web && npx playwright install chromium`. Tests use temporary databases and simulated upstreams; they do not make real provider calls. Release scripts prepare local artifacts without publishing.

## Source and license

Account protocols and parts of the Antigravity adapter are based on or adapted from [sub2api](https://github.com/Wei-Shaw/sub2api). FiftyAPI is licensed under LGPL-3.0. Keep [LICENSE](LICENSE), [COPYING.GPL-3](COPYING.GPL-3), [THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md) and the [source fingerprints](docs/sub2api-source.json).

See [Contributing](CONTRIBUTING.md), [Security](SECURITY.md), [Changelog](CHANGELOG.md) and [release maintenance](docs/en/maintaining.md). Runtime databases, master keys and user credentials are excluded from releases.
