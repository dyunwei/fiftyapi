CREATE TABLE account_contribution_links (
 id INTEGER PRIMARY KEY,
 token_hash BLOB NOT NULL UNIQUE,
 actor_id INTEGER NOT NULL REFERENCES users(id),
 provider TEXT NOT NULL CHECK(provider IN ('openai','claude','antigravity')),
 note TEXT NOT NULL DEFAULT '',
 created_at INTEGER NOT NULL,
 expires_at INTEGER NOT NULL,
 revoked_at INTEGER
);
ALTER TABLE upstream_accounts ADD COLUMN contribution_link_id INTEGER REFERENCES account_contribution_links(id);
ALTER TABLE upstream_accounts ADD COLUMN auth_fingerprint BLOB;
CREATE UNIQUE INDEX account_auth_fingerprint ON upstream_accounts(auth_fingerprint) WHERE auth_fingerprint IS NOT NULL AND deleted_at IS NULL;
