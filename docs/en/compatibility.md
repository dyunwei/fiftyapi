# Compatibility and verification scope

[简体中文](../compatibility.md) | English

This page summarizes current protocols, boundaries and verification evidence recorded in the repository. Catalog visibility does not establish inference access; implemented endpoints and simulated tests do not establish support across all accounts, models or third-party deployments. See [low-maintenance sharing](low-maintenance.md) for configuration and [client setup](clients.md) for connection instructions.

## Current protocols

| Upstream source | Protocol and endpoint | Boundary |
| --- | --- | --- |
| Claude OAuth | Messages: `/v1/messages` | metadata/system pass through by default; `count_tokens` is a local estimate |
| OpenAI Codex OAuth | Responses: HTTP JSON/SSE and native WebSocket | compact is implemented, but recorded live tests returned 404; it is not verified as available |
| Antigravity OAuth | Native `v1internal:generateContent` / `v1internal:streamGenerateContent` | Messages conversion was removed; clients must support the native envelope |
| API key gateway | Explicitly declared Chat Completions, Messages, Responses | No automatic conversion between standard protocols; WebSocket needs explicit enablement and upstream support |

An API model can declare multiple protocols with one public ID and price set; the requested protocol must have an available route. Chat forwards native JSON/SSE, tools and extension fields only to Chat-capable API upstreams. It does not convert other protocol responses into Chat.

Responses HTTP/SSE uses complete history. WebSocket supports prewarming and continuation within one connection. Requests execute sequentially per connection; reconnecting requires complete history. Steer and explicit cancel messages are unsupported. Client connections receive a Ping every 30 seconds; missing Pong for 10 seconds closes the connection, cancels in-flight work and releases the connection slot. This does not establish upstream inference health.

## Pricing, settlement and routing boundaries

- Budgets use fixed-point reference USD price snapshots, not actual OAuth subscription charges or remaining provider quota. Missing model or requested-tier prices prevent calls.
- Automatic prices support explicitly listed Priority/Fast, Ultrafast, Flex, cache and long-context rates. Manual prices support the standard tier only. Not every model offers these tiers; see [pricing rules](low-maintenance.md).
- Cancellation and interruption settle reliable usage already received. Missing or invalid usage, or usage that cannot be priced from the snapshot, settles at zero and releases reservations. This does not imply the provider charged nothing. Ledger write failures retain accounting evidence, but historical failures alone do not block subsequent calls; see [automatic settlement](administration.md#automatic-settlement).
- Priority, weights, bounded FIFO waiting, account/model cooldown and conditional session binding are supported. OAuth and API sources for the same public model can share a routing pool. Switching occurs only under safe conditions; signed/encrypted histories retain their account binding, and uncertain execution is not blindly replayed.
- Image/audio generation, payments, paid plans, multiple instances and automatic model downgrade are outside scope. Paid built-in tools and additional image/audio/video billing dimensions are not fully implemented; field forwarding does not establish full feature or cost support.

## Recorded live acceptance

These are existing repository records. This documentation cleanup does not rerun live acceptance or cover personal tests whose details are not recorded here.

| Source | Client identities, models and scope | Limitations |
| --- | --- | --- |
| OpenAI Codex OAuth | Discovery, text, SSE tool round trips, cancellation and accounting with `gpt-5.6-luna` and `gpt-6.1-sol`; WebSocket prewarming and continuation with `gpt-6.1-sol`; a short prompt and isolated multi-tool workflow with native Codex `0.164.0-alpha.11.1` | Recorded identities include `0.146.0` and `0.164.0`; compact returned 404 with both. Real authorization-code exchange, token rotation and long-duration real-project workflows lack complete acceptance records |
| Antigravity OAuth (historical converter) | Authorization, refresh, discovery, text/SSE, tool round trips and internal accounting with `gemini-3.5-flash-lite` | Used the former Messages converter; does not establish compatibility of the current native v1internal path. Native client version was not recorded |
| Claude OAuth | No recorded live-account or native Claude Code acceptance | Current records cover simulations |
| Third-party API key gateways | No recorded live third-party deployment acceptance | Local simulations and chained FiftyAPI gateways do not establish compatibility with arbitrary Sub2Api/New API deployments |

Dates, identities, models and reproduction conditions are in [OpenAI acceptance](../openai-acceptance.md), in Chinese. Historical Antigravity usage, early acceptance and per-change records remain in the [archive](compatibility-history.md). External client versions are distinct from FiftyAPI versions.

## Automated verification scope

Tests use simulated upstreams and temporary databases by default without consuming live-provider quota. Live-provider tests are opt-in and require authorization before execution.

| Area | Existing coverage and conditions |
| --- | --- |
| Protocols and accounts | Responses JSON/SSE/WebSocket, Messages, native Chat and Antigravity v1internal; OAuth refresh rotation, route isolation, cooldown and safe failover |
| Pricing and accounting | Immutable snapshots, tier/cache rates, idempotent settlement, cancellation/truncation, zero settlement, recovery, reservation release and user concurrency limits |
| Request diagnostics | Simulated OpenAI, Codex identity `0.164.0`, fixture model `model`; protocol mismatch, budget blocking, throttling, failed WebSocket handshakes, SSE first-output and queue timing |
| Antigravity catalog tests | 2026-11-18, identity `2.23.0`, fixture model `native-probe-model`; native previews, default inference origin, 429/RetryInfo, safe redaction and no duplicate invocation |
| Client transport checks | Python 3.11 helper, local gateway and simulated HTTP; Responses/Messages SSE timing, event coalescing detection and WebSocket handshakes. Covers client-to-gateway transport without native clients or model inference |
| Administration and operations | Setup, roles/CSRF, migrations, credential encryption, log isolation, mail and backup restoration; Chinese/English browser flows and mobile layouts |

These simulations establish no additional live-provider compatibility. Catalog synchronization, candidate client identities and client-to-gateway transport checks do not establish model inference acceptance.

## Capacity and deployment

Designed for small teams using one instance and SQLite. Local simulated concurrency and accounting checks do not establish fixed production capacity; capacity depends on upstream concurrency, request duration, disk and historical data volume. Existing load tests do not fully cover long streams, large historical logs, concurrent administration or production disks. See [SQLite load tests](../sqlite-load-test.md), in Chinese, for conditions and results.

## Language scope

Console controls, help, accessibility labels, dates and fixed diagnostic summaries support Chinese and English. Language selection is stored in the browser. `/api/` errors honor supported `Accept-Language` preferences; omitted or unsupported preferences retain Chinese. Invitations and test emails contain both languages.

User input, model IDs, credentials, raw upstream information and model responses retain their original content. Terminal logs and detailed engineering records may remain in Chinese.
