-- Preserve source identity across repeated imports without merging unrelated users.
CREATE TABLE IF NOT EXISTS imported_users (
 source TEXT NOT NULL,
 source_user_id INTEGER NOT NULL,
 user_id INTEGER NOT NULL REFERENCES users(id),
 PRIMARY KEY(source,source_user_id)
);
