# Client configuration

[简体中文](../clients.md) | English

Create your own API key in the console. Clients use that `fifty_…` key, not an administrator password or upstream OAuth token. Replace local URLs below with your site origin. LAN deployments may use HTTP; HTTPS is recommended for public access. Use complete model IDs from `/v1/models`.

## Choose the protocol

| Model source | Client mode | Endpoint |
| --- | --- | --- |
| Claude OAuth, Antigravity, Messages-only API model | Anthropic Messages | `/v1/messages` |
| Codex OAuth, Responses API model | OpenAI Responses | `/v1/responses` |
| API model explicitly supporting Chat | OpenAI Chat Completions | `/v1/chat/completions` |
| Multi-protocol API model | Any declared mode, using the same model ID | Corresponding endpoint |

Standard protocols are not automatically converted. Model names alone do not identify protocol support. Some clients ignore catalog extensions and require manual selection. Antigravity Gemini models use Anthropic Messages mode.

## Claude Code / Messages

```sh
export ANTHROPIC_BASE_URL=http://127.0.0.1:8080
export ANTHROPIC_AUTH_TOKEN='YOUR_FIFTYAPI_KEY'
export ANTHROPIC_MODEL='MODEL_ID_FROM_CATALOG'
claude
```

The base URL has no `/v1`; the client appends `/v1/messages`. Clear conflicting vendor `ANTHROPIC_API_KEY` settings. Messages routes to Claude or Antigravity; the latter uses a dedicated adapter for requests, tools and SSE. `/v1/messages/count_tokens` is a local estimate, not exact vendor tokenization.

Real Antigravity tool round trips passed for `gemini-3.5-flash-lite`. Real Claude OAuth and the native Claude Code client remain unverified.

## Codex / Responses

Add a custom provider to `~/.codex/config.toml`, preserving other settings:

```toml
model_provider = "fiftyapi"
model = "MODEL_ID_FROM_CATALOG"

[model_providers.fiftyapi]
name = "fiftyapi"
base_url = "http://127.0.0.1:8080/v1"
env_key = "FIFTYAPI_API_KEY"
wire_api = "responses"
requires_openai_auth = false
supports_websockets = true
```

```sh
export FIFTYAPI_API_KEY='YOUR_FIFTYAPI_KEY'
codex
```

The client key belongs to FiftyAPI; the server handles upstream authorization. Imported OpenAI credentials have passed core HTTP/SSE and tool tests, plus a short native Codex/WebSocket test with client `0.164.0-alpha.11.1`. Long-running tool workflows require further validation.

HTTP/SSE uses `store:false` and full history. Native WebSocket supports `previous_response_id` within the same connection; reconnecting requires complete history. Requests on one connection are sequential; separate connections may run concurrently. API key upstreams require explicit WebSocket enablement and actual upstream support; otherwise set `supports_websockets=false`.

The compact endpoint is implemented but availability must be confirmed separately. With the older client identity, the tested account returned HTTP 404; compact with the newer identity remains unverified. Do not assume automatic context compaction will work.

## API examples

```sh
export FIFTYAPI_API_KEY='YOUR_FIFTYAPI_KEY'
curl -sS http://127.0.0.1:8080/v1/models \
  -H "Authorization: Bearer $FIFTYAPI_API_KEY"
```

For a Chat-capable API model:

```sh
curl -N http://127.0.0.1:8080/v1/chat/completions \
  -H "Authorization: Bearer $FIFTYAPI_API_KEY" \
  -H 'Content-Type: application/json' \
  -d '{"model":"YOUR_CHAT_MODEL_ID","max_tokens":64,"stream":true,"stream_options":{"include_usage":true},"messages":[{"role":"user","content":"Reply OK"}]}'
```

For a Messages model:

```sh
curl -N http://127.0.0.1:8080/v1/messages \
  -H "Authorization: Bearer $FIFTYAPI_API_KEY" \
  -H 'Content-Type: application/json' \
  -d '{"model":"YOUR_MESSAGES_MODEL_ID","max_tokens":64,"stream":true,"messages":[{"role":"user","content":"Reply OK"}]}'
```

Chat and Responses usually use a base URL ending in `/v1`; Messages usually uses the origin. Avoid `/v1/v1/responses`. A client's own provider UUID prefix is not part of a gateway model ID.

## Troubleshooting

Models need administrator-confirmed prices, an available upstream and a positive user budget. HTTP 401 suggests a key/user problem; 400 suggests model, pricing or protocol configuration; 429 suggests local budget or upstream rate limits; 503 suggests account availability, concurrency or session binding.

Reliable received usage is settled after cancellation or interruption. Missing/invalid usage releases the reservation at zero local cost, which does not imply zero upstream cost. Provide request IDs, status and sanitized errors when asking for help; never share keys or OAuth callbacks. See [compatibility](compatibility.md).
