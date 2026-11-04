CREATE TABLE upstream_accounts_new (
 id INTEGER PRIMARY KEY,
 name TEXT NOT NULL UNIQUE,
 provider TEXT NOT NULL CHECK(provider IN ('claude','openai','antigravity','gateway')),
 credentials BLOB NOT NULL,
 enabled INTEGER NOT NULL DEFAULT 1 CHECK(enabled IN (0,1)),
 state TEXT NOT NULL CHECK(state IN ('authorized','project_pending','catalog_ready','ready','reauth_required','error')),
 last_error TEXT NOT NULL DEFAULT '',
 created_at INTEGER NOT NULL,
 updated_at INTEGER NOT NULL,
 gateway_platform TEXT NOT NULL DEFAULT ''
);
INSERT INTO upstream_accounts_new(id,name,provider,credentials,enabled,state,last_error,created_at,updated_at)
SELECT id,name,provider,credentials,enabled,state,last_error,created_at,updated_at FROM upstream_accounts;
DROP TABLE upstream_accounts;
ALTER TABLE upstream_accounts_new RENAME TO upstream_accounts;
INSERT INTO settings(key,value) VALUES('relay_instance_id',lower(hex(randomblob(16))));
