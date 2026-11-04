# Maintenance and releases

[简体中文](../maintaining.md) | English

The first public version is **0.1.0**, tagged `v0.1.0`. Earlier internal iterations are not part of the public release history. Keep existing schema migrations and compatibility fields when changing the public version.

## Build and release

Keep `internal/buildinfo/VERSION`, `web/package.json` and the root/package versions in `web/package-lock.json` synchronized. During 0.x development, increment the minor version for incompatible behavior or non-reversible schema changes and the patch version for fixes.

1. Update versions and the bilingual `CHANGELOG.md`, including migration and configuration implications.
2. Run `make test`, `make build` and `make test-ui`. Install Chromium with `cd web && npx playwright install chromium` if needed.
3. Run `./scripts/build-release.sh` to prepare local artifacts. The binary targets the current build machine's OS/architecture. The package includes both README languages, both quickstarts, documentation, licenses, dependency notices and `SHA256SUMS`.
4. Commit/push and wait for CI. For the first release, create and push the annotated tag `v0.1.0`.
5. Draft a GitHub release with the changelog and attach the prepared files. Packaging alone does not publish anything.

## Translations

UI locale selection supports `zh-CN` and `en`, follows supported browser preferences and persists in local storage. English is the fallback when browser languages are unsupported. API error defaults remain Chinese for compatibility.

Use complete Chinese messages as `t()` keys and put English translations in `web/src/locales/en.json`. Use positional placeholders such as `{0}` rather than concatenating translated fragments. Shared operational diagnostics are in `internal/i18n/en.json`, embedded by Go and imported by the frontend. Translate diagnostic fields only; preserve names, notes, model IDs, credentials and response bodies.

Keep English user guides under `docs/en/` and `README.md` aligned with Chinese documentation. Run catalog/unit tests and both-language browser flows. Rebuild and commit `internal/server/ui` after frontend changes.

## Source and credentials

Retain LICENSE, COPYING.GPL-3, THIRD_PARTY_NOTICES.md and `docs/sub2api-source.json`. Binary/image distribution must provide corresponding complete source and build materials. Update dependency notices when dependencies change.

Do not commit `.local/`, `data/`, `.env`, databases, master keys, setup tokens, OAuth files or real API keys. Run `python3 scripts/check-source.py`; it checks release sources without inspecting ignored runtime directories. Credentials accidentally published must be revoked/rotated even if the files are later deleted.

Enable private vulnerability reporting on the GitHub repository and follow [SECURITY.md](../../SECURITY.md). Require CI for pull requests, keep dependency updates reviewed, and ask issue reporters for versions and sanitized reproduction details.
