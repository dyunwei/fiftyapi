CREATE TABLE api_keys (
 id INTEGER PRIMARY KEY, user_id INTEGER NOT NULL REFERENCES users(id),
 name TEXT NOT NULL, prefix TEXT NOT NULL, token_hash BLOB NOT NULL UNIQUE,
 enabled INTEGER NOT NULL DEFAULT 1, created_at INTEGER NOT NULL
);
CREATE TABLE models (
 id TEXT PRIMARY KEY, provider TEXT NOT NULL, upstream_id TEXT NOT NULL,
 enabled INTEGER NOT NULL DEFAULT 1, synced_at INTEGER NOT NULL,
 UNIQUE(provider,upstream_id)
);
CREATE TABLE account_models (
 account_id INTEGER NOT NULL REFERENCES upstream_accounts(id), model_id TEXT NOT NULL REFERENCES models(id),
 PRIMARY KEY(account_id,model_id)
);
CREATE TABLE model_prices (
 id INTEGER PRIMARY KEY, model_id TEXT NOT NULL REFERENCES models(id),
 input INTEGER NOT NULL CHECK(input>=0), output INTEGER NOT NULL CHECK(output>=0),
 cached INTEGER NOT NULL CHECK(cached>=0), cache_write INTEGER NOT NULL CHECK(cache_write>=0),
 created_at INTEGER NOT NULL
);
CREATE TABLE quota_windows (
 id INTEGER PRIMARY KEY, user_id INTEGER NOT NULL REFERENCES users(id),
 kind TEXT NOT NULL CHECK(kind IN ('5h','7d')), active INTEGER NOT NULL DEFAULT 1,
 started_at INTEGER, expires_at INTEGER, used INTEGER NOT NULL DEFAULT 0, reserved INTEGER NOT NULL DEFAULT 0,
 CHECK(used>=0 AND reserved>=0)
);
CREATE UNIQUE INDEX quota_active_window ON quota_windows(user_id,kind) WHERE active=1;
CREATE TABLE request_charges (
 id TEXT PRIMARY KEY, user_id INTEGER NOT NULL REFERENCES users(id), key_id INTEGER NOT NULL REFERENCES api_keys(id),
 model_id TEXT NOT NULL, window_5h INTEGER NOT NULL REFERENCES quota_windows(id), window_7d INTEGER NOT NULL REFERENCES quota_windows(id),
 reservation INTEGER NOT NULL, amount INTEGER NOT NULL DEFAULT 0,
 status TEXT NOT NULL CHECK(status IN ('inflight','pending','settled')),
 price_snapshot TEXT NOT NULL, created_at INTEGER NOT NULL, settled_at INTEGER
);
CREATE INDEX charges_pending ON request_charges(status,created_at);
CREATE TABLE request_logs (
 id TEXT PRIMARY KEY, user_id INTEGER NOT NULL REFERENCES users(id), key_id INTEGER REFERENCES api_keys(id),
 account_id INTEGER REFERENCES upstream_accounts(id), model TEXT NOT NULL, provider TEXT NOT NULL,
 status INTEGER NOT NULL, input_tokens INTEGER, output_tokens INTEGER, cached_tokens INTEGER, cache_write_tokens INTEGER,
 amount INTEGER, duration_ms INTEGER NOT NULL, error TEXT NOT NULL DEFAULT '', created_at INTEGER NOT NULL
);
CREATE INDEX logs_user_time ON request_logs(user_id,created_at DESC);
CREATE INDEX logs_time ON request_logs(created_at DESC);
CREATE TABLE settings (key TEXT PRIMARY KEY,value TEXT NOT NULL);
INSERT INTO settings(key,value) VALUES('log_retention_days','30');
ALTER TABLE admin_audit_logs ADD COLUMN details TEXT NOT NULL DEFAULT '';
