CREATE TABLE proxy_servers (
 id INTEGER PRIMARY KEY,
 protocol TEXT NOT NULL CHECK(protocol IN ('http','https','socks5','socks5h')),
 host TEXT NOT NULL,
 port INTEGER NOT NULL CHECK(port BETWEEN 1 AND 65535),
 location TEXT NOT NULL DEFAULT '',
 note TEXT NOT NULL DEFAULT '',
 username TEXT NOT NULL DEFAULT '',
 password BLOB,
 healthy INTEGER,
 latency_ms INTEGER,
 last_error TEXT NOT NULL DEFAULT '',
 checked_at INTEGER,
 created_at INTEGER NOT NULL,
 updated_at INTEGER NOT NULL
);

ALTER TABLE upstream_accounts ADD COLUMN proxy_id INTEGER REFERENCES proxy_servers(id);
ALTER TABLE account_contribution_links ADD COLUMN proxy_id INTEGER REFERENCES proxy_servers(id);
