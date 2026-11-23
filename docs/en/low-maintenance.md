# Low-maintenance account sharing

[简体中文](../low-maintenance.md)

FiftyAPI uses native protocols, automatic catalogs, and reference prices to allocate internal budgets. Reference USD is a budget unit, not a payment system or an exact conversion of OAuth subscription allowances.

## Native connections

- API key upstreams use their declared Chat Completions, Messages, and Responses protocols.
- Claude OAuth preserves client metadata and system instructions by default. Identity normalization and prompt injection are explicit options.
- Codex OAuth uses Responses. Unknown fields, sampling parameters, and output limits are retained for upstream validation. Necessary OAuth store/stream, device identity, and transport handling remain. JSON/SSE/WebSocket stay within the original protocol.
- Antigravity no longer converts Messages. Use `POST /v1internal:streamGenerateContent` or `POST /v1internal:generateContent` with a native envelope containing `model` and `request`. Project and credentials belong to the selected account; model aliases are mapped. Other request fields and native responses are preserved. Clients must support this protocol; Anthropic Messages clients cannot directly use this source. A custom inference URL must match the desired native action.

Example, authenticated with a local Bearer key:

```json
{"model":"model-id","request":{"contents":[{"role":"user","parts":[{"text":"hi"}]}],"generationConfig":{"maxOutputTokens":64}}}
```

## Automatic models and prices

Healthy account catalogs continue to synchronize daily. Production downloads LiteLLM's `model_prices_and_context_window.json` daily as data only. It neither executes LiteLLM code nor requires a LiteLLM server. Failed syncs retain local prices and retry after an hour. Settings → Automatic maintenance shows state and offers manual sync.

Matching is exact. Native Codex uses `openai/upstream-id`, Claude uses `anthropic/upstream-id`, and other sources use exact catalog keys. For aliases or unmatched models, choose a key under Edit price → Track price catalog. Newly discovered models can use the downloaded catalog immediately. Unpriced models remain unavailable.

Imported text prices include input, output, cache reads, cache writes, Anthropic one-hour cache writes, and explicitly supplied Priority/Fast, Ultrafast, Flex, and 128k/200k/272k long-context variants. `priority` and `fast` normalize to one tier. Coverage depends on each entry; not every model has all tier or cache prices.

Raw token counts are never multiplied. Admission reserves budget using conservative rates for the requested tier. Settlement prefers the served tier reported by upstream; absent tier information uses the requested tier and is labeled accordingly in logs. Observed context size and cache duration select the settlement rate. Each request keeps immutable complete pricing rules and source revision. Manual prices override synchronization and cover Standard only; selecting a catalog key resumes automatic pricing.

Unpriced tiers are rejected before dispatch. Missing or invalid usage, or a missing price discovered after dispatch, settles at zero, releases reservations, and records the reason. Administrators can review ledger write failures. Settlement exceptions do not block later requests. Confirmed unexecuted rejections settle at zero. Reliable usage observed before interruption is still charged.

Money is parsed into fixed-point integers. Unrepresentable rates are not rounded into free prices. Reservations are estimates, not hard upstream generation caps, and final usage may exceed them. Paid built-in tools, image/audio/video, and regional surcharges are not fully metered; text reference cost is not a complete supplier invoice for those requests.

## User controls

Member Access limits configure model allowlists and concurrency. Allow-all includes future priced models; an empty allowlist denies all. Concurrency zero delegates to upstream account limits. Positive limits are checked atomically during reservation and shared across HTTP/SSE/WebSocket. Existing per-user 5-hour and 7-day budgets remain shared by all keys. There is no separate permission table for speed tiers: more expensive models and speeds consume more budget.

## Client identity

Settings → Automatic maintenance centralizes Codex, Claude Code, and Antigravity defaults. Blank account versions inherit; explicit versions remain pinned. Saving refreshed credentials preserves inheritance. Codex discovery and inference use the same selected-account identity; downstream version query parameters do not change the native discovery identity.

Stable OpenAI Codex GitHub releases are checked daily as candidates. Discovery does not activate a version or run billable compatibility probes. Administrators can update or roll back defaults without deployment. Identity and protocol changes can still require maintenance; candidate versions are not verified compatibility claims.

`FIFTYAPI_AUTO_MAINTENANCE=false` disables background price/release network checks while retaining local data and manual sync. Daily inference probes are off by default and require `FIFTYAPI_MORNING_PROBES=true`. Account catalog synchronization and necessary OAuth refresh remain governed by account maintenance.

## Data and verification

Append-only migrations 019–021 preserve encrypted accounts, master-key bindings, and historical price snapshots. No database reset is required. Static bundled prices and the Antigravity converter were removed. Historical records remain readable; historical zero settlements are not retroactively changed.

This refactor is verified using temporary databases and simulated upstreams, default Codex identity 0.164.0, and fixture model IDs such as `model`. Coverage includes Responses JSON/SSE, Messages, native Antigravity, catalog synchronization, tier prices, caches, snapshots, recovery, and concurrency/access rules. No live-provider acceptance was run. No new real-client compatibility is established for native Antigravity or identity changes. Historical converter acceptance does not apply to the native path.

On 2026-11-17, a separate parse check downloaded the public LiteLLM price file: 4,171,564 bytes yielded 4,190 catalog keys, including 577 keys with valid Fast rates, 433 with Flex, and 20 with Ultrafast. Keys include aliases, are not unique model counts, and do not establish inference compatibility. SHA-256: `63050115948432fd1abf146ad4f0670acb612163f98e084ceed1522ec210a2d5`. `make test`, `make build`, and `make test-ui` passed, including 27 browser tests.

## Diagnosing local Antigravity tests

The catalog's Test action builds a native `v1internal` envelope with `requestId`, `userAgent`, `requestType`, and `request.contents`, rather than Messages. Once results arrive, the preview uses the server-built request while omitting account authentication and project details. Default inference uses `daily-cloudcode-pa.googleapis.com`; discovery retains its configured endpoint. Explicit inference URLs are preserved and custom sites never automatically switch to an official origin.

HTTP 429 on this test means the selected upstream restricted the request. It does not indicate a local user budget failure or by itself establish missing model permission. When supplied, known Google status/ErrorInfo codes and Retry-After/RetryInfo hints are displayed. Raw error messages, projects, and quota metadata remain private. No automatic retry or model switch occurs; another request requires the administrator to click Test again.

The 2026-11-18 fix corrects stale copy, the preview, and the default inference-origin regression. Simulated tests use Antigravity identity 2.23.0 and fixture model `native-probe-model`, covering native envelopes, 429/RetryInfo, private-data protection, and single dispatch. No live provider was contacted, so this does not establish that a real account's 429 has cleared.

Validation passed: `make test`, `make build`, and `make test-ui`, including 28 browser tests.

Use “Use candidate version” to save the detected version as the default. The previous Codex default is retained for “Revert to previous version”; reverting again switches back. Manual version changes also update the rollback value; saving the same version preserves it. Pinned account versions are unaffected. These actions neither install clients nor update protocol implementations or run paid probes.

Client WebSockets receive protocol Pings every 30 seconds. A missing Pong within 10 seconds ends the connection, cancels active work and releases connection slots. Terminal/container logs record connection opening, read termination, release, upstream reuse and idle duration. Correlate them with failure logs using `connection_id`. This heartbeat checks client-to-gateway connectivity, not upstream inference availability.

Upstream WebSockets retain a reader between turns to handle Ping/Pong and close frames promptly. A Ping is sent every 30 seconds; a 10-second timeout clears the connection and its response ID cache. After detected closure, the next independent request without `previous_response_id` can open a new connection. Continuations using an old connection's response ID explicitly require the client to resend full history; context is never silently reconstructed. Disconnects after dispatch are not automatically replayed. Closure can still race with sending, and upstream failures remain possible.

Client limits remain four connections per user and 128 globally. At the user limit, a new connection may replace the same user's oldest idle connection only after 30 seconds without requests and with no queued or executing turns. Busy or recently used connections remain protected and admission still returns 429 when none can be replaced. Connections idle for about five minutes are closed automatically; protocol heartbeats do not reset business idle time. Quota refusals, invalid requests and enqueue failures release activity markers. Reclamation disconnects the old socket and clears its upstream response context; clients must reconnect and cannot reuse response IDs from that socket. Logs distinguish `reason=replaced_idle` and `idle_timeout`.

WebSocket admission reserves user and global capacity together, lets `coder/websocket` validate and complete the handshake, then commits any idle replacement. A failed handshake rolls back its reservation and preserves the old connection. Replacement reuses its original slot, so it does not require a 129th slot at global capacity. If the old connection resumes work or has just completed a request during the handshake, the new socket closes with 1013 for retry while preserving the old connection and its work. Handshake writes have a five-second deadline. Logged connection counts represent unified logical admission slots; retiring sockets are not counted twice.
