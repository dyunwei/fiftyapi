# FiftyAPI repository instructions

These instructions apply to the whole repository.

## Working safely

- Preserve unrelated user changes. Do not rewrite or discard a dirty worktree.
- Never commit databases, `.local/`, `data/`, `.env`, setup tokens, master keys,
  OAuth sessions, authorization files, or real provider credentials.
- Tests must use simulated upstreams by default. Live-provider acceptance tests
  are opt-in and require explicit user approval because they may refresh tokens
  or consume paid quota.
- Keep SQLite migrations append-only. Add a new numbered migration instead of
  editing a migration that may already have been applied.

## Code and documentation

- Run `gofmt` on changed Go files and keep network calls outside SQLite write
  transactions.
- Preserve the accounting invariants: fixed-point money, idempotent settlement,
  immutable price snapshots, and release of reservations on every terminal path.
- When changing UI copy, update `web/src/locales/en.json`. When changing shared
  API diagnostics, update `internal/i18n/en.json` as applicable.
- Keep Chinese and English user documentation aligned when behavior changes.
- Frontend source changes require rebuilding and committing `internal/server/ui`
  so the Go binary continues to embed the current UI.

## Verification

- Run `make test` before handoff.
- For frontend changes, also run `make build` and `make test-ui`.
- Do not claim live-provider compatibility from simulated tests. Record the
  tested client version, protocol, model, and limitations in the compatibility
  documentation.
