-- Antigravity no longer advertises an emulated Messages endpoint.
UPDATE account_models SET protocols='["antigravity"]' WHERE account_id IN (SELECT id FROM upstream_accounts WHERE provider='antigravity');
UPDATE models SET protocols='["antigravity"]' WHERE provider='antigravity' AND NOT EXISTS(SELECT 1 FROM account_models am JOIN upstream_accounts a ON a.id=am.account_id WHERE am.model_id=models.id AND a.provider<>'antigravity');
