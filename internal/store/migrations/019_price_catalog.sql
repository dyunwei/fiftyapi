-- External price data and immutable model price versions. Existing credentials
-- and ledger records are preserved; applied migrations remain append-only.
ALTER TABLE model_prices ADD COLUMN source TEXT NOT NULL DEFAULT 'manual';
ALTER TABLE model_prices ADD COLUMN rules TEXT NOT NULL DEFAULT '{}';
ALTER TABLE request_logs ADD COLUMN billing_usage TEXT NOT NULL DEFAULT '{}';
CREATE TABLE price_catalog (
    key TEXT PRIMARY KEY,
    snapshot TEXT NOT NULL,
    revision TEXT NOT NULL,
    synced_at INTEGER NOT NULL
);
CREATE TABLE maintenance_state (
    name TEXT PRIMARY KEY,
    last_success INTEGER NOT NULL DEFAULT 0,
    last_attempt INTEGER NOT NULL DEFAULT 0,
    error TEXT NOT NULL DEFAULT '',
    revision TEXT NOT NULL DEFAULT ''
);
ALTER TABLE models ADD COLUMN pricing_key TEXT NOT NULL DEFAULT '';
