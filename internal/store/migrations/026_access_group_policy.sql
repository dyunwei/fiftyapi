-- NULL marks groups whose members still use their pre-upgrade individual policies.
ALTER TABLE access_groups ADD COLUMN models TEXT;
ALTER TABLE access_groups ADD COLUMN concurrency INTEGER CHECK(concurrency BETWEEN 0 AND 100);

UPDATE access_groups SET models='null',concurrency=0 WHERE NOT EXISTS(
 SELECT 1 FROM user_access WHERE group_id=access_groups.id AND (models<>'null' OR concurrency<>0)
);
