# Compatibility and verification

[简体中文](../compatibility.md) | English

These are the boundaries of the **0.1.1 public preview**. Implemented endpoints and mock tests do not establish that every upstream account or third-party deployment supports them.

| Source | Standard protocol | Real verification |
| --- | --- | --- |
| Claude OAuth | Messages | Simulated coverage; real account and native Claude Code not yet tested |
| OpenAI Codex OAuth | Responses HTTP/SSE and native WebSocket | Imported-account discovery, text, tools, cancellation, accounting, WebSocket, a short native prompt and an isolated multi-tool Codex workflow passed |
| Antigravity OAuth | Messages through a dedicated adapter | `gemini-3.5-flash-lite` refresh, discovery, SSE, tool calls/results and accounting passed |
| API key gateways | Declared Chat Completions, Messages and/or Responses | Local TLS mock upstreams and chained FiftyAPI gateways; real Sub2Api/New API sites remain unverified |

The compact endpoint exists, but the tested OpenAI account returned HTTP 404 with both the older 0.146.0 and newer 0.164.0 client identities. Real OpenAI authorization-code exchange and refresh-token rotation, long-duration real-project workflows and real Claude authorization remain outstanding; the bounded isolated multi-tool Codex workflow has passed.

## Protocol behavior

Chat is forwarded only to Chat-capable API upstreams. Claude and Antigravity use Messages; Codex OAuth uses Responses. There is no automatic Chat → Messages conversion or arbitrary conversion between standard protocols. A multi-protocol API model shares one ID and one price set; unsupported endpoints are rejected before upstream invocation and budget reservation.

Chat forwards native JSON/SSE, tools, extensions and reported usage, replacing the mapped model ID. It does not aggregate forced SSE into Chat JSON or convert other protocol responses into Chat. Streams must complete normally; truncation does not synthesize a success marker.

HTTP Responses uses complete stateless history. Native WebSocket supports prewarming and continuation within one connection. Requests within a connection execute sequentially; context is not shared across connections. API key upstreams need explicit native WebSocket enablement. Connection loss requires complete history; steer and explicit cancellation messages are not implemented.

Catalog visibility is not inference verification. Accounts can lack model or protocol permissions even when a model is listed. Local model tests consume upstream quota and should be initiated knowingly by administrators.

## Accounting and scheduling

Prices are internal USD snapshots. Missing prices prevent calls. Reliable usage received before cancellation or interruption is settled; missing or invalid usage releases reservations at zero local cost. This does not establish that upstream execution was free or that partial usage equals the provider's final measurement.

Priority, weighted health/load selection, bounded FIFO waiting, account/model cooldown and conditional session binding are implemented. OAuth accounts and API key gateway sources that expose the same public model ID can participate in one routing pool while retaining their own upstream model IDs. Switching accounts is allowed only under safe retry conditions. Signed/encrypted histories remain bound, and requests with uncertain upstream execution are not blindly replayed.

A local simulation with 50 independent users passed; this is not a fixed production-capacity guarantee. Real capacity depends on account concurrency and request duration. Deploy one instance with SQLite on persistent local disk.

Images/audio generation, payments, paid plans, priority/flex pricing, paid built-in tool surcharges, complex grouping and automatic model downgrade are outside scope.

## Language scope

Console controls, help text, forms, accessibility labels, dates and fixed diagnostic summaries support Chinese and English. Your selection is stored locally. API errors under `/api/` honor supported `Accept-Language` preferences; omitted/unsupported preferences retain Chinese for existing clients. Invitations and test emails contain both languages.

User names, notes, model IDs, credentials, arbitrary upstream messages and model responses retain their original content. Operational messages remain unchanged in storage and are translated for display; terminal logs may retain their source language. Detailed engineering and historical acceptance records remain available in Chinese.

## Automated and real evidence

Automated tests cover initialization, sessions/CSRF/roles, migrations, credential encryption, token rotation, routing and safe retry, protocol isolation, settlement and recovery, logs, mail and backup restoration. Browser tests exercise Chinese and English flows and mobile layouts using temporary databases and simulated upstreams. Real-provider acceptance tests are opt-in.

The [Chinese acceptance records](../openai-acceptance.md) preserve tested client identities, dates, models and usage-accounting conditions. External client version numbers in those records are distinct from FiftyAPI's public version.
