# Migrate from sub2api

During administrator setup, choose **Migrate from sub2api Docker**. The administrator is created and signed in automatically, then a four-step wizard covers export, upload, review/import and verification. Commands, file transfer instructions, rules and next actions appear on the page; no separate guide is required. Unfinished onboarding resumes after refresh or sign-in; select uploaded files again. Completing or skipping onboarding closes the import capability; normal administration pages have no migration entry point. This is a scoped migration of users, original keys and supported accounts, not a full database restore.

## Default budgets

Self-registered users start at $0 in both budget windows. Newly invited members, imported members and the initial administrator receive $100 for each of the 5-hour and 7-day windows. Invitations can override these defaults, including explicitly setting zero. These are shared rolling spending limits, not a one-time balance. Source balances, plans and used amounts are not converted. Existing FiftyAPI users and budgets are unchanged.

## Export, preview and cut over

1. On the source Docker host, run the one-line command shown in the wizard:

   ```sh
   curl -fsSL https://raw.githubusercontent.com/dyunwei/fiftyapi/main/scripts/migrate-sub2api.sh | sudo bash
   ```

   The self-contained [Bash script](../../scripts/migrate-sub2api.sh) checks Docker, discovers running PostgreSQL containers, prompts if several are found, exports a read-only consistent snapshot and prints the file path. It uses the container's client and credentials, including password secret files. No host Python/psql installation, dependency downloads, source changes, service stops or data uploads occur. Choose the old database container rather than the application container.

   Output is a new private `/tmp/fiftyapi-sub2api-XXXXXX.json` file (0600). Under sudo, ownership is returned to the invoking user. Source identity derives automatically from the Docker engine, container name and database name for repeatable imports. Passwords, tokens and full keys are never printed. Limits remain 31 MiB, 5000 users, 20000 keys, 1000 accounts and 1000 proxies; exports fail rather than truncate.

   If GitHub is unavailable, download the embedded script directly from the wizard, transfer it to the source server and run `sudo bash migrate-sub2api.sh`. Use `--container my-postgres` for a custom or noninteractive container. Transfer the JSON to your browser computer with an existing SSH/SFTP client, or select it directly if your browser runs on the server. Keep PostgreSQL running during final export.

   Advanced deployments can still use [export-sub2api.py](../../scripts/export-sub2api.py) with Python, host psql and standard PG connection settings, or its original Docker mode: `python3 scripts/export-sub2api.py --docker --source my-sub2api-production --output /tmp/sub2api-migration.json`. Reuse a stable source identifier for this advanced tool; store passwords in a private `.pgpass` / `PGPASSFILE`, not command arguments. The ordinary web flow needs no advanced tool.

2. Upload the JSON in FiftyAPI and select **Preview migration**. Preview saves nothing and never contacts upstreams or refreshes tokens. Review skipped and disabled items. The file is not displayed or stored in browser storage.
3. The web wizard preselects conversion of keys with source groups, IP rules, independent quotas, rolling limits or expiry to FiftyAPI global model access and member budgets, discarding those restrictions, including future expiry dates. Uncheck the option to import those keys disabled. Source-disabled or already expired keys stay disabled. Conversion affects newly inserted keys only; retrying cannot re-enable existing keys. If originally imported disabled, keep them disabled or create new FiftyAPI keys and update clients.
4. Before the final export, stop source requests, management writes and token refresh jobs. Re-export and preview the final file, acknowledge cutover, and confirm migration. Users and keys commit in one transaction; accounts save independently. Inspect per-item results and retry the same file if interrupted. Existing users, passwords, budgets and key status are preserved.
5. Select **Verify and sync imported accounts**, review failures, then confirm models and local prices. Manual gateway model sync validates local configuration, not real upstream access. Unpriced models cannot be called. OAuth mappings, source group aliases and advanced routing may require reconfiguration.
6. Test the original member email/password and an original API key, then switch the domain or client URL. Clients can keep their keys. Keeping the domain, protocols and model names compatible can avoid client changes. Source administrators import as regular members.

The migration file contains password hashes, full API keys, upstream tokens and proxy passwords. Transfer privately and delete after use. Keep a source backup and stop source refresh jobs after cutover. For rollback, stop FiftyAPI first; rotated OAuth tokens may no longer work in the source backup and may require separate recovery.

## Compatibility and conflicts

- Source email becomes the login name; username becomes the display name. Compatible bcrypt hashes retain the original password. Users with MFA or incompatible hashes, including some external-login accounts, are skipped.
- Original 16–128-byte visible ASCII keys are stored encrypted and authenticated by hash. Newly generated FiftyAPI keys keep their usual format. Keys never attach to an unrelated user when an owner fails to migrate.
- A stable source/user-ID mapping prevents duplicate users. Email/name conflicts are skipped without merging users. Repeated imports preserve existing settings and add only missing keys.
- Supports Claude/Anthropic, OpenAI/Codex and Antigravity OAuth, preserving authorization, expiry, applicable client/workspace identifiers and Antigravity project ID.
- Anthropic/OpenAI `apikey` or `api_key` accounts become HTTPS gateways, using Messages or Chat/Responses respectively. Explicit model mappings without wildcards are supported. Gemini CLI, Bedrock, service accounts, cookies, shadow accounts and other types are skipped.
- Linked proxies must exist, be active and unexpired, and use HTTP, HTTPS, SOCKS5 or SOCKS5H. Missing/incompatible proxies cause account skips, never a silent direct connection. Proxy fallback and expiry switching are not imported.
- Disabled/expired upstream accounts stay disabled. Concurrency is capped at 100. Priority is clamped to `0–100`; both schedulers prefer smaller values. Existing account names or credentials are skipped without overwriting configuration.
- Excludes balances, plans, history, prices, groups, user platform permissions, sessions, MFA secrets, email/system configuration, custom headers and other advanced policies. Imported members use FiftyAPI global model access and budgets; review source user-level access differences before cutover.

Admin API: `POST /api/admin/migrations/sub2api`, with an administrator session, CSRF token, pending initialization onboarding and JSON body `{ "data": bundle, "preview": true/false, "accept_key_policy": false }`. Request limit: 32 MiB. Responses contain only per-item status and counts, never credentials.

## Sign-in fails after import

Use the original email and check that the imported member is enabled. Compatible bcrypt hashes are copied unchanged; migration does not assign a shared password. Repeated imports preserve the current FiftyAPI password, and now explicitly report differences from the uploaded export. Different hashes can also represent the same password with different salts. If sign-in still fails, an administrator should reset the password or restore a verified source record; never overwrite all member passwords automatically.

The login form reads actual input values at submission, including password-manager autofill without input events. Hard-refresh pages opened before an upgrade, or use a private window, to load the updated UI.

### Password preservation

The browser parses JSON without hashing `password_hash` again. The server decodes the string and binds it directly to SQL. The transaction reads the saved value back byte for byte and rolls back the user/key batch on mismatch. Successful imports record only `password_hash_preserved=true` in the audit summary. Browser regression checks file/request/database equality and signs in using the original fixture password. These checks do not repair an existing database or overwrite a locally changed password.
