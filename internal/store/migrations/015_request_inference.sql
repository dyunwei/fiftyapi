ALTER TABLE request_logs ADD COLUMN reasoning_effort TEXT NOT NULL DEFAULT '';
ALTER TABLE request_logs ADD COLUMN thinking_type TEXT NOT NULL DEFAULT '';
ALTER TABLE request_logs ADD COLUMN thinking_budget_tokens INTEGER;
ALTER TABLE request_logs ADD COLUMN speed TEXT NOT NULL DEFAULT '';
ALTER TABLE request_logs ADD COLUMN service_tier TEXT NOT NULL DEFAULT '';
