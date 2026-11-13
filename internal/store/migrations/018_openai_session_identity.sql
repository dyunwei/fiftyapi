CREATE TABLE openai_account_devices (
 account_id INTEGER PRIMARY KEY REFERENCES upstream_accounts(id),
 device_id TEXT NOT NULL UNIQUE
);

ALTER TABLE gateway_sessions ADD COLUMN hold_until INTEGER NOT NULL DEFAULT 0;
