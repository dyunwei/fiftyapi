# Client configuration

[简体中文](../clients.md) | English

Create your own API key in the console. Clients use that `sk-…` key, not an administrator password or upstream OAuth token. Replace local URLs below with your site origin. LAN deployments may use HTTP; HTTPS is recommended for public access. Use complete model IDs from `/v1/models`.

For Codex or Claude Code, you can also copy an [automatic setup command](#automatic-client-setup) from “Use key” in the console.

## Choose the protocol

| Model source | Client mode | Endpoint |
| --- | --- | --- |
| Claude OAuth, Messages-only API model | Anthropic Messages | `/v1/messages` |
| Codex OAuth, Responses API model | OpenAI Responses | `/v1/responses` |
| API model explicitly supporting Chat | OpenAI Chat Completions | `/v1/chat/completions` |
| Multi-protocol API model | Any declared mode, using the same model ID | Corresponding endpoint |

Standard protocols are not automatically converted. Model names alone do not identify protocol support. Some clients ignore catalog extensions and require manual selection. Antigravity requires a native v1internal client; see [low-maintenance sharing](low-maintenance.md).

## Claude Code / Messages

```sh
export ANTHROPIC_BASE_URL=http://127.0.0.1:8080
export ANTHROPIC_AUTH_TOKEN='YOUR_FIFTYAPI_KEY'
export ANTHROPIC_MODEL='MODEL_ID_FROM_CATALOG'
claude
```

The base URL has no `/v1`; the client appends `/v1/messages`. Clear conflicting vendor `ANTHROPIC_API_KEY` settings. Messages routes to Claude or Messages-capable API upstreams. Antigravity uses its native v1internal protocol. `/v1/messages/count_tokens` is a local estimate, not exact vendor tokenization.

Historical Antigravity converter tool tests do not establish compatibility for the new native path. Real Claude OAuth and the native Claude Code client remain unverified.

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

Reliable received usage is settled after cancellation or interruption. Missing/invalid usage releases reservations and requires administrator settlement; further calls by that user pause until resolution. Provide request IDs, status and sanitized errors when asking for help; never share keys or OAuth callbacks. See [compatibility](compatibility.md).

## Inference access status

The member overview shows which budget windows have no allocation, are exhausted, or have their remaining budget held by in-flight requests. An exhausted active window shows its expected reset time. If a pending settlement blocks inference, open the affected request's diagnostics directly from the overview. Refresh the page after an administrator resolves the settlement or after in-flight requests finish.

## Client connection check

Copy the command for your platform and client from **Use API key → Client connection check**. You can also download the current `/setup.py` from your site and run `python3.11 fiftyapi-setup.py check codex` or `python3.11 fiftyapi-setup.py check claude`. On Windows use `py -3.11`.

Run it as the same user and in the same terminal environment used to configure the client. The helper compares the local configuration with the saved setup and checks connectivity, API key acceptance and model protocol through `/v1/models`. It does not edit configuration, send inference requests or print secrets. For Codex it reports whether WebSocket is configured. Open **Request diagnostics** in the request log to see the observed transport. [Request diagnostics](administration.md#request-diagnostics)

The gateway sends three local SSE events on the client API path, and the helper reports the gaps between arrivals. If events arrive together, check reverse proxy buffering or compression. For Codex, the helper also verifies a WebSocket upgrade on `/v1/responses`, then disconnects without sending an inference request. A failed WebSocket handshake fails the check when WebSocket is enabled in Codex. Claude Code checks SSE only. The SSE check requires an updated gateway; older gateways may return HTTP 400 or 405. Results cover the network path used by the helper. Upstream WebSocket support and model inference require separate verification.

## Automatic client setup

Select a client, OS and model under “Use key”, then copy the automatic setup command. Requires Python 3.11+ (`python3.11` on macOS/Linux, `py -3.11` on Windows). The current site serves the auditable helper at `/setup.py`. No administrator access or client installation is required.

For VS Code, run in a new terminal in the window using Codex. Remote SSH, WSL and Dev Containers configure the user in that environment; do not use sudo. Persist the user directory to survive container recreation. The helper respects `CODEX_HOME` and `CLAUDE_CONFIG_DIR`. Reload the window and start a new session afterwards. An extension may bundle a different client version from the CLI on PATH.

The key is entered at a hidden prompt. The only network request is `/v1/models` to validate the key, model and protocol; no inference is sent. Redirects are rejected to avoid forwarding credentials. Use HTTPS publicly. Codex requires Responses; Claude Code requires Messages. WebSocket is enabled only when explicitly selected.

Codex persists the key using `experimental_bearer_token`, allowing a desktop-launched VS Code to read it without shell environment variables or changes to `auth.json`. Official documentation discourages this field for general credential management; this helper uses plaintext local storage for extension access. Claude Code merges user settings.json env and maps all three model aliases to the selected model. Conflicting inherited auth, endpoint or cloud-provider environment variables must be cleared first. Project or managed settings may still override user settings. The Claude Code VS Code extension remains unverified.

Other parsed settings are preserved. The helper rewrites Codex TOML using section headers, with one `[projects."path"]` section per project and comments retained in the original file backup. Every write backs up the full original bytes. Restore handles only managed fields, refusing conflicts with edits by other tools. A newly created file is removed on restore if no other settings remain.

The page shows automatic setup by default; file-editing instructions are under “Manual setup (optional)”.

“Restore previous connection settings” restores the model, endpoint and authentication settings from before the first FiftyAPI setup, preserving later changes to other settings. If you previously used the official Codex service, that connection is restored. This is different from resetting the client to factory defaults. Full file backups are also saved before each change, but selecting a historical backup for automatic restore is not currently supported. The page generates a copyable restore command for the selected client and OS.

The helper is saved at `~/.fiftyapi-helper/helper.py`:

```sh
python3.11 ~/.fiftyapi-helper/helper.py status codex
python3.11 ~/.fiftyapi-helper/helper.py configure codex
python3.11 ~/.fiftyapi-helper/helper.py restore codex
python3.11 ~/.fiftyapi-helper/helper.py use codex
python3.11 ~/.fiftyapi-helper/helper.py uninstall codex
```

Replace `codex` with `claude` for Claude Code. On Windows use, for example, `py -3.11 "$HOME\.fiftyapi-helper\helper.py" restore codex`. Configure can change the site, key and model while retaining the original connection fields for restore. Each operation handles one client.

Config, recovery state and backups may contain keys. They stay local and must not be shared or committed. New Unix files are owner-only; on Windows use a private user profile and check ACLs. Uninstall restores managed fields and removes that client's recovery state. Removing all clients also removes the helper, retaining `~/.fiftyapi-helper/backups/` for manual cleanup. After a crash, ensure no helper is running before removing the `operation.lock` directory and retrying.

### Verification scope

2026-11-14: Python 3.11 tests cover simulated model catalogs (Responses / Messages), repeated configuration, restore/re-enable, conflict refusal, unrelated settings and symlink refusal. Local Codex CLI **0.169.0-alpha.11.1** parsed a persistent-auth config with a simulated model using `features list` in an isolated CODEX_HOME; no inference was sent. Windows, macOS, Remote SSH, VS Code extensions and native Claude Code have not received end-to-end acceptance testing. No real DeepSeek/Kimi compatibility is claimed. See the existing [compatibility record](compatibility.md).
