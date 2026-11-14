# Automatic client setup

Select a client, OS and model under “Use key”, then copy the automatic setup command. Requires Python 3.11+ (`python3.11` on macOS/Linux, `py -3.11` on Windows). The current site serves the auditable helper at `/setup.py`. No administrator access or client installation is required.

For VS Code, run in a new terminal in the window using Codex. Remote SSH, WSL and Dev Containers configure the user in that environment; do not use sudo. Persist the user directory to survive container recreation. The helper respects `CODEX_HOME` and `CLAUDE_CONFIG_DIR`. Reload the window and start a new session afterwards. An extension may bundle a different client version from the CLI on PATH.

The key is entered at a hidden prompt. The only network request is `/v1/models` to validate the key, model and protocol; no inference is sent. Redirects are rejected to avoid forwarding credentials. Use HTTPS publicly. Codex requires Responses; Claude Code requires Messages. WebSocket is enabled only when explicitly selected.

Codex persists the key using `experimental_bearer_token`, allowing a desktop-launched VS Code to read it without shell environment variables or changes to `auth.json`. Official documentation discourages this field for general credential management; this helper uses plaintext local storage for extension access. Claude Code merges user settings.json env and maps all three model aliases to the selected model. Conflicting inherited auth, endpoint or cloud-provider environment variables must be cleared first. Project or managed settings may still override user settings. The Claude Code VS Code extension remains unverified.

Other parsed settings are preserved, but TOML comments and formatting are normalized. Every write backs up the full original bytes. Restore handles only managed fields, refusing conflicts with edits by other tools. A newly created file is removed on restore if no other settings remain.

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

## Verification scope

2026-11-14: Python 3.11 tests cover simulated model catalogs (Responses / Messages), repeated configuration, restore/re-enable, conflict refusal, unrelated settings and symlink refusal. Local Codex CLI **0.169.0-alpha.11.1** parsed a persistent-auth config with a simulated model using `features list` in an isolated CODEX_HOME; no inference was sent. Windows, macOS, Remote SSH, VS Code extensions and native Claude Code have not received end-to-end acceptance testing. No real DeepSeek/Kimi compatibility is claimed. See the existing [compatibility record](compatibility.md).
