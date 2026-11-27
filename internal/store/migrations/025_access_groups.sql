CREATE TABLE access_groups (
 id INTEGER PRIMARY KEY AUTOINCREMENT,
 name TEXT NOT NULL UNIQUE COLLATE NOCASE
);

ALTER TABLE user_access ADD COLUMN group_id INTEGER REFERENCES access_groups(id) ON DELETE SET NULL;
CREATE INDEX user_access_group ON user_access(group_id) WHERE group_id IS NOT NULL;

ALTER TABLE upstream_accounts ADD COLUMN group_restricted INTEGER NOT NULL DEFAULT 0 CHECK(group_restricted IN (0,1));
CREATE TABLE account_access_groups (
 account_id INTEGER NOT NULL REFERENCES upstream_accounts(id) ON DELETE CASCADE,
 group_id INTEGER NOT NULL REFERENCES access_groups(id) ON DELETE CASCADE,
 PRIMARY KEY(account_id, group_id)
);
CREATE INDEX account_access_groups_group ON account_access_groups(group_id);
