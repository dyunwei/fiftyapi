-- A public model is a logical routing pool.  The concrete upstream model and
-- protocol capabilities belong to each account route, not to the pool.
ALTER TABLE account_models ADD COLUMN upstream_id TEXT NOT NULL DEFAULT '';
ALTER TABLE account_models ADD COLUMN protocols TEXT NOT NULL DEFAULT '[]';

UPDATE account_models
SET upstream_id = COALESCE((SELECT upstream_id FROM models WHERE models.id = account_models.model_id), model_id),
    protocols = COALESCE((SELECT protocols FROM models WHERE models.id = account_models.model_id), '[]');

CREATE INDEX account_models_model ON account_models(model_id, account_id);
