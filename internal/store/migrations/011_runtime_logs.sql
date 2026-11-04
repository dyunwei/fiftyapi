CREATE TABLE runtime_logs (
 id INTEGER PRIMARY KEY AUTOINCREMENT,
 level TEXT NOT NULL,
 module TEXT NOT NULL,
 event TEXT NOT NULL,
 account_id INTEGER NOT NULL DEFAULT 0,
 message TEXT NOT NULL,
 status INTEGER NOT NULL DEFAULT 0,
 duration_ms INTEGER NOT NULL DEFAULT 0,
 occurrences INTEGER NOT NULL DEFAULT 1,
 created_at INTEGER NOT NULL,
 last_at INTEGER NOT NULL,
 bucket INTEGER NOT NULL,
 UNIQUE(level,module,event,account_id,message,status,bucket)
);
CREATE INDEX runtime_logs_time ON runtime_logs(last_at DESC,id DESC);
CREATE INDEX runtime_logs_account ON runtime_logs(account_id,last_at DESC);
