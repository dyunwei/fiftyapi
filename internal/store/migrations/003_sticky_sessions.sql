CREATE TABLE gateway_sessions (
 session_hash BLOB PRIMARY KEY,
 user_id INTEGER NOT NULL REFERENCES users(id),
 account_id INTEGER NOT NULL REFERENCES upstream_accounts(id),
 expires_at INTEGER NOT NULL
);
CREATE INDEX gateway_sessions_expiry ON gateway_sessions(expires_at);
