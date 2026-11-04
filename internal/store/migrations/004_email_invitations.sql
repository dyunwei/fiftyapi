ALTER TABLE users ADD COLUMN email TEXT COLLATE NOCASE;
ALTER TABLE users ADD COLUMN display_name TEXT NOT NULL DEFAULT '';
ALTER TABLE users ADD COLUMN password_set INTEGER NOT NULL DEFAULT 1 CHECK(password_set IN (0,1));
CREATE UNIQUE INDEX users_email_unique ON users(email) WHERE email IS NOT NULL;
CREATE TABLE user_invitations (
 user_id INTEGER PRIMARY KEY REFERENCES users(id),
 token_hash BLOB NOT NULL UNIQUE,
 expires_at INTEGER NOT NULL,
 accepted_at INTEGER,
 issued_at INTEGER NOT NULL,
 mail_state TEXT NOT NULL CHECK(mail_state IN ('sending','sent','failed')),
 mail_error TEXT NOT NULL DEFAULT ''
);
