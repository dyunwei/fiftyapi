# Administration

[简体中文文档索引](../README.md) | English

## Members and budgets

Initialize the administrator with the deployment setup token. Afterwards members can register with email, real name and an 8–72-byte password. Registration requires no SMTP and does not verify email ownership. New members always start with zero budget; they cannot choose their own role or limits. Administrators allocate budgets and can send email invitations instead.

Every user's keys and providers share two dollar budgets: 5 hours and 7 days. The first positive-cost settlement starts a window; unused budget does not accumulate. Increasing a limit does not reset usage. Explicit window resets are audited, and accepted requests still settle against their original windows.

A request reserves estimated cost, then settles reliable reported usage against its original price snapshot. Interrupted requests settle any reliable usage received. Missing or invalid usage settles at zero and releases the reservation; this is a local cost-allocation policy and does not imply the upstream was free. Ledger or price-snapshot failures remain settlement exceptions for administrators to review under **Request logs**. Retrying settlement does not call the model again.

## Upstream accounts

Use **Upstream accounts** to add Claude Code, OpenAI Codex or Antigravity OAuth accounts, or select **+ Connect upstream API** for API key gateways. OAuth links expire after 10 minutes; paste the full callback URL or `code#state`. If localhost cannot load the callback, copy its URL from the address bar.

Authorization JSON imports accept up to 20 files totaling 1 MiB. Only credential fields are imported; embedded URLs, proxies and headers are not. Credentials are encrypted, never displayed or stored in browser storage. Avoid having several services refresh the same rotating token.

**Contribution links** let account owners submit their credentials without signing in. Links are provider-specific, valid for 1–168 hours (24 by default), usable by multiple owners and revocable. Full links are shown only once. Contributions start disabled; review settings and enable accounts before verification. Expiry or revocation does not remove accounts already submitted.

Connection settings include inference URL overrides, proxy, request timeout, client identity and permitted headers. Proxy passwords remain masked; preserving the mask preserves the password. Verify and sync after changing settings.

**Scheduling & health** controls priority, weight, concurrency and queue limits. Lower numeric priority wins; weights distribute new sessions among similarly healthy accounts. Waiting is bounded. Signed/encrypted context stays bound to its account; switching accounts requires a safe rejection before content is emitted. Requests with uncertain execution are not blindly replayed. Clearing cooldown is audited and does not replenish upstream quota.

## API key gateways and model protocols

Supported presets include Sub2Api, FiftyAPI, New API and other compatible gateways. Use a Base URL such as `https://gateway.example/v1`, not a concrete endpoint. HTTPS is required unless trusted-network HTTP is explicitly enabled. Redirects are not followed with credentials. The default authentication mode is Bearer; `x-api-key` is also available.

Automatic discovery reads `/v1/models`, supporting up to 10 pages and 1000 upstream models. A catalog supplies IDs, not proof of protocol permissions. Declare the upstream's actual default protocols: Chat Completions, Messages and/or Responses. Per-model mappings can override these defaults, replace the upstream ID and set a unique local alias. An override replaces the default protocol set rather than adding to it.

```json
{"id":"my-model","upstream_id":"upstream-model","protocols":["chat","responses"]}
```

This model works through Chat and Responses with one local ID and shared prices, but cannot be called using Messages. Re-sync after protocol changes. Legacy IDs and prices are retained where possible; a protocol name embedded in an ID does not override its `protocols` metadata.

Manual mode saves configured models without querying the catalog and does not verify upstream connectivity or permissions. Prices for gateway models must be supplied locally. The gateway does not read upstream billing balances or automatically apply vendor prices to gateway models.

Responses WebSocket and compact are separate opt-in capabilities. Enable them only when the upstream supports them. Standard protocols are not automatically converted. Relay chains detect direct loops between FiftyAPI instances, but third-party gateways may drop the marker; keep the topology acyclic.

## Models and prices

**View models** shows the account's saved catalog, sync time, search, pagination, original upstream IDs, pricing and protocol status. Disabled or unpriced models remain visible to administrators. Opening the catalog does not send inference requests. Failed sync retains the previous successful snapshot; successful empty catalogs replace it.

**Models & pricing** uses USD per million tokens, separately for input, output, cache reads and cache writes. Unpriced models are not callable. Historical requests retain their original prices. Prices express local budget accounting, not OAuth subscription invoices.

**Test** sends `hi` to the selected account/model without requiring local pricing or charging local users. It still consumes upstream quota, uses the account's concurrency and adapter settings, and can take up to 90 seconds. The response display is limited to 256 KiB. This explicit test is separate from model catalog synchronization.

Enabled OpenAI OAuth accounts with synced models also receive a daily short `hi` test at 07:00 in the service timezone. The task selects the first catalog model by ID, using Responses. It attempts to start the upstream quota window; upstream rules determine the actual result. Missed tasks are not replayed after downtime. Results appear in runtime logs without response bodies or credentials.

Optional usage protection caps reasoning effort (`low`, `medium`, `high`), explicit thinking token budgets (1024–1048576, zero unlimited), or forces standard speed. Codex has no independent hard thinking-token cap. Claude adaptive thinking uses effort; explicit thinking uses a token budget. Antigravity maps effort to local budget caps of 2048/8192/24576 and uses the lower configured limit. These settings do not guarantee exact token consumption or equivalent vendor effort levels.

## Email invitations

Under **System settings → Email settings**, configure the public HTTP or HTTPS site origin (LAN IPs and hostnames are supported), SMTP host, port, sender, username/password and TLS mode. STARTTLS commonly uses 587, TLS commonly uses 465; plaintext is restricted to local testing. Enable invitations and save, then send a test message.

Saved web configuration takes precedence over environment variables and applies immediately. Passwords are encrypted and never returned. Leave the password blank to retain it; select Clear to delete it. Restoring environment settings removes the web override. Environment changes themselves require restart.

Fallback variables are `FIFTYAPI_PUBLIC_URL`, `FIFTYAPI_SMTP_HOST`, `FIFTYAPI_SMTP_PORT`, `FIFTYAPI_SMTP_FROM`, `FIFTYAPI_SMTP_USERNAME`, `FIFTYAPI_SMTP_PASSWORD` and `FIFTYAPI_SMTP_TLS`.

Invitations send single-use password setup links valid for 24 hours, in both Chinese and English. Resending invalidates the previous link. No password is sent by email. SMTP acceptance does not guarantee inbox delivery; inspect member delivery status and retry after fixing configuration.

## Logs and security

Members see only their own request logs; administrators see all requests and settlement exceptions. Logs include model, status, token counts, cost and duration, excluding full prompts and responses. Retention defaults to 30 days and can be 1–3650 days; deleting logs does not reset budget accounting. Filters and pagination run server-side. **Routing details** shows individual account attempts for one logical request and one settlement.

**Runtime logs** retain fixed operational summaries for 7 days. Matching events within 5 minutes are combined with occurrence counts and the latest duration. **Audit log** records administrator actions. Logs redact keys, tokens and raw upstream responses. Early startup errors can appear only in terminal/container output.

Changing a password revokes existing sign-in sessions. Members can revoke or delete their own keys; deletion immediately prevents future access while preserving historical settlement relationships.

## Backups

Under **System settings → Backups**, configure an S3-compatible HTTPS origin, region, existing bucket, key prefix and credentials. Cloudflare R2 uses region `auto` and an endpoint such as `https://<account_id>.r2.cloudflarestorage.com`. Path style is supported. Credentials are encrypted; blank fields retain saved values.

**Test connection** uploads and deletes a small file using the current form values. Creating backups uses saved settings. Downloading requires object read access in addition to write/delete permissions.

Automatic backups run daily at the configured service-local time. If today's time has passed without a run, saving or restarting starts one catch-up run. A failed automatic attempt is not automatically recreated that day; manual backup remains available. Retention is 1–3650 days, default 14, and affects future backups only. Manual backups can use their own retention.

The service uses `VACUUM INTO` to create a consistent SQLite snapshot and verifies integrity. A single `tar.gz` archive contains `fiftyapi.db` and `manifest.json`, compatible with the offline restoration tool. Only one task runs at a time; the archive limit is 5 GiB. Task timeout is 30 minutes and a single storage request can take up to 15 minutes. Reserve enough space in the database volume for temporary snapshots and archives.

Database credentials remain encrypted, but the archive itself receives no additional encryption and contains user metadata. Restrict bucket access. **The master key, environment variables and external files are not included; retain the original master key separately.**

Expired objects are deleted in the background and failed deletions retried. Storage versioning may retain old versions; configure storage-side lifecycle rules if needed. Records retain their original storage configuration, so keep old credentials valid until old backups have been handled. Interrupted tasks become failed after restart.

To restore a downloaded archive, stop the service, extract it into a trusted directory and restore into a new directory:

```sh
mkdir /secure/backup-extracted
tar -xzf fiftyapi-<time>-<id>.tar.gz -C /secure/backup-extracted
python3 scripts/backup-offline.py restore \
  --from /secure/backup-extracted \
  --output /secure/restored-data
```

Start with that database and the original master key. The snapshot excludes later changes. Review restored backup schedules and settings, and do not let the old and restored instances manage the same remote backups simultaneously.
