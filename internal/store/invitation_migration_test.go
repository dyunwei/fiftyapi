package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"io/fs"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestEmailMigrationKeepsExistingLoginSessionsAndKeys(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "legacy.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	scripts, err := fs.Glob(migrations, "migrations/00[123]_*.sql")
	if err != nil || len(scripts) != 3 {
		t.Fatal("legacy migration fixture missing")
	}
	for index, file := range scripts {
		script, err := migrations.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = db.ExecContext(ctx, string(script)); err != nil {
			t.Fatal(err)
		}
		if _, err = db.ExecContext(ctx, "INSERT INTO schema_migrations(version,applied_at) VALUES(?,?)", index+1, time.Now().Unix()); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = db.ExecContext(ctx, "INSERT INTO users(id,username,password_hash,role,limit_5h,limit_7d,created_at) VALUES(1,'legacy','retained-hash','user',100,1000,?)", time.Now().Unix()); err != nil {
		t.Fatal(err)
	}
	sessionHash := sha256.Sum256([]byte("legacy-session"))
	if _, err = db.ExecContext(ctx, "INSERT INTO sessions(token_hash,user_id,csrf_token,expires_at) VALUES(?,1,'retained-csrf',?)", sessionHash[:], time.Now().Add(time.Hour).Unix()); err != nil {
		t.Fatal(err)
	}
	token := "fifty_" + strings.Repeat("a", 43)
	keyHash := sha256.Sum256([]byte(token))
	if _, err = db.ExecContext(ctx, "INSERT INTO api_keys(user_id,name,prefix,token_hash,created_at) VALUES(1,'legacy','fifty_legacy',?,?)", keyHash[:], time.Now().Unix()); err != nil {
		t.Fatal(err)
	}
	db.Close()
	s, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	user, err := s.UserByName(ctx, "legacy")
	if err != nil || !user.PasswordSet || user.PasswordHash != "retained-hash" || user.Email != nil {
		t.Fatal("legacy account changed during migration")
	}
	if _, csrf, err := s.Session(ctx, sessionHash[:]); err != nil || csrf != "retained-csrf" {
		t.Fatal("legacy session invalidated")
	}
	if u, _, err := s.AuthenticateAPIKey(ctx, token); err != nil || u.Username != "legacy" {
		t.Fatal("legacy key invalidated")
	}
	users, err := s.Users(ctx)
	if err != nil || len(users) != 1 || users[0].InvitationState != "" {
		t.Fatal("legacy user incorrectly treated as pending invitation")
	}
}
