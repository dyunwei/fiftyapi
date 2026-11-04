# Third-party source notices

## sub2api

Source: https://github.com/Wei-Shaw/sub2api (local reference checkout used for this project).

The Antigravity protocol conversion files under `internal/compat/antigravity` are adapted from `backend/internal/pkg/antigravity` in sub2api. Original contributors retain their copyrights. The upstream LGPL version 3 license is reproduced in `LICENSE`, with the incorporated GPL version 3 text in `COPYING.GPL-3`. See `docs/sub2api-source.json` for original paths and content hashes.

Local changes: imports for converter debug logging use a no-output logger so malformed request/response content is not logged. Subsequent adaptation changes are documented alongside the implementation and in the migration notes. Distribution of modified source and build material is intended to remain under the included LGPL terms.

Platform OAuth defaults also reference the local upstream implementation. The Antigravity desktop OAuth client ID and paired shared client parameter are platform configuration already present in upstream source; they are not a user's Google password, access token, or refresh token. Deployment environment overrides take precedence. User/account credentials are encrypted at rest and must never be included in releases.

## Dependencies

Go dependencies and exact checksums are recorded in `go.mod` and `go.sum`; frontend dependencies are recorded in `web/package.json` and `web/package-lock.json`. Their respective upstream license notices must accompany distributions as applicable. Build scripts will collect dependency notices for release artifacts; the repository does not include account runtime data.

Responses WebSocket transport uses `github.com/coder/websocket` v1.8.14 (ISC license). Its upstream license is collected into `dist/licenses/github.com_coder_websocket/LICENSE.txt` by the release build; exact filename is listed in the generated license index. Account routing and lifecycle behavior also reference the local sub2api services, with project-specific SQLite accounting and bounded queues implemented in this repository.
