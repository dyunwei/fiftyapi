ALTER TABLE models ADD COLUMN protocols TEXT NOT NULL DEFAULT '[]';
UPDATE models SET protocols=CASE
 WHEN provider='openai' OR provider LIKE 'gateway/%/responses' THEN '["responses"]'
 WHEN provider='claude' OR provider='antigravity' OR provider LIKE 'gateway/%/messages' THEN '["messages"]'
 WHEN provider LIKE 'gateway/%/chat' THEN '["chat"]'
 ELSE '[]' END;
