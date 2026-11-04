-- Retain a tombstone for charge/log foreign keys, but erase usable key metadata.
ALTER TABLE api_keys ADD COLUMN deleted_at INTEGER;
CREATE INDEX model_prices_latest ON model_prices(model_id,id DESC);
