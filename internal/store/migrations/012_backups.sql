CREATE TABLE backup_records (
 id TEXT PRIMARY KEY,
 status TEXT NOT NULL,
 filename TEXT NOT NULL,
 object_key TEXT NOT NULL,
 target BLOB NOT NULL,
 size_bytes INTEGER NOT NULL DEFAULT 0,
 sha256 TEXT NOT NULL DEFAULT '',
 trigger_kind TEXT NOT NULL,
 actor_id INTEGER NOT NULL DEFAULT 0,
 started_at INTEGER NOT NULL,
 completed_at INTEGER,
 expires_at INTEGER NOT NULL,
 error TEXT NOT NULL DEFAULT '',
 scheduled_day TEXT UNIQUE
);
CREATE INDEX backup_expiry ON backup_records(status,expires_at);
