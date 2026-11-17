CREATE TABLE user_access (
 user_id INTEGER PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
 models TEXT NOT NULL DEFAULT 'null',
 concurrency INTEGER NOT NULL DEFAULT 0 CHECK(concurrency BETWEEN 0 AND 100)
);
CREATE INDEX request_charges_user_status ON request_charges(user_id,status);
