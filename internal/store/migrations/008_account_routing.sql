ALTER TABLE upstream_accounts ADD COLUMN deleted_at INTEGER;
CREATE TABLE account_policies (
 account_id INTEGER PRIMARY KEY REFERENCES upstream_accounts(id),
 initialized INTEGER NOT NULL DEFAULT 0,
 priority INTEGER NOT NULL DEFAULT 50 CHECK(priority BETWEEN 0 AND 100),
 weight INTEGER NOT NULL DEFAULT 1 CHECK(weight BETWEEN 1 AND 100),
 concurrency INTEGER NOT NULL DEFAULT 5 CHECK(concurrency BETWEEN 1 AND 100),
 max_queue INTEGER NOT NULL DEFAULT 16 CHECK(max_queue BETWEEN 0 AND 1000),
 wait_ms INTEGER NOT NULL DEFAULT 2000 CHECK(wait_ms BETWEEN 0 AND 30000)
);
INSERT INTO account_policies(account_id) SELECT id FROM upstream_accounts;
CREATE TABLE account_health (
 account_id INTEGER PRIMARY KEY REFERENCES upstream_accounts(id),
 failures INTEGER NOT NULL DEFAULT 0, error_rate REAL NOT NULL DEFAULT 0,
 first_output_ms REAL NOT NULL DEFAULT 0, last_status INTEGER NOT NULL DEFAULT 0,
 last_success INTEGER, last_failure INTEGER, last_reason TEXT NOT NULL DEFAULT '',
 primary_used REAL, primary_reset INTEGER, secondary_used REAL, secondary_reset INTEGER, quota_observed_at INTEGER
);
INSERT INTO account_health(account_id) SELECT id FROM upstream_accounts;
CREATE TABLE account_cooldowns (
 account_id INTEGER NOT NULL REFERENCES upstream_accounts(id), scope TEXT NOT NULL DEFAULT '',
 until_at INTEGER NOT NULL, reason TEXT NOT NULL, PRIMARY KEY(account_id,scope)
);
CREATE TABLE request_attempts (
 request_id TEXT NOT NULL REFERENCES request_charges(id), attempt INTEGER NOT NULL,
 account_id INTEGER NOT NULL REFERENCES upstream_accounts(id), model TEXT NOT NULL,
 status INTEGER NOT NULL, duration_ms INTEGER NOT NULL, first_output_ms INTEGER,
 dispatched INTEGER NOT NULL, retryable INTEGER NOT NULL, reason TEXT NOT NULL,
 created_at INTEGER NOT NULL, PRIMARY KEY(request_id,attempt)
);
CREATE INDEX attempts_account_time ON request_attempts(account_id,created_at DESC);
