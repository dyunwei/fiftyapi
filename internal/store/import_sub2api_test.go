package store

import (
	"bytes"
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"
)

func TestSub2APIUsersKeysPreviewOwnershipAndRetry(t *testing.T) {
	ctx := context.Background()
	s, err := Open(ctx, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	admin, err := s.CreateAdmin(ctx, "admin", "hash")
	if err != nil {
		t.Fatal(err)
	}
	a, err := s.UserByName(ctx, "admin")
	if err != nil || a.Limit5h != DefaultMemberBudget || a.Limit7d != DefaultMemberBudget {
		t.Fatal("new administrator budget", err)
	}
	hash, _ := bcrypt.GenerateFromPassword([]byte("original-password"), bcrypt.MinCost)
	users := []MigrationUser{{ID: 1, Email: "MEMBER@example.com", PasswordHash: string(hash), Status: "active"}, {ID: 2, Email: "disabled@example.com", PasswordHash: string(hash), Status: "disabled"}, {ID: 3, Email: "totp@example.com", PasswordHash: string(hash), Status: "active", TOTPEnabled: true}}
	group := int64(8)
	past := time.Now().Add(-time.Hour)
	keys := []MigrationKey{
		{ID: 1, UserID: 1, Name: "original", Key: "legacy-original-key-fixture", Status: "active"},
		{ID: 2, UserID: 1, Name: "restricted", Key: "legacy-restricted-key-fixture", Status: "active", GroupID: &group},
		{ID: 3, UserID: 2, Name: "disabled owner", Key: "legacy-disabled-user-fixture", Status: "active"},
		{ID: 4, UserID: 3, Name: "totp", Key: "legacy-totp-user-key-fixture", Status: "active"},
		{ID: 5, UserID: 1, Name: "expired", Key: "legacy-expired-key-fixture", Status: "active", ExpiresAt: &past},
	}
	master := bytes.Repeat([]byte{7}, 32)
	plan, err := s.ImportSub2APIUsers(ctx, admin, "old-site", users, keys, master, true, false)
	if err != nil {
		t.Fatal(err)
	}
	var n int
	s.DB.QueryRow("SELECT count(*) FROM imported_users").Scan(&n)
	if n != 0 {
		t.Fatal("preview persisted mappings")
	}
	if _, err = s.UserByName(ctx, "member@example.com"); err == nil {
		t.Fatal("preview persisted user")
	}
	if len(plan) != 8 || plan[4].Enabled || plan[2].Status != "skipped" {
		t.Fatalf("bad preview: %+v", plan)
	}
	out, err := s.ImportSub2APIUsers(ctx, admin, "old-site", users, keys, master, false, false)
	if err != nil {
		t.Fatal(err)
	}
	u, err := s.UserByName(ctx, "member@example.com")
	if err != nil || u.Role != "user" || u.Limit5h != DefaultMemberBudget || u.Limit7d != DefaultMemberBudget || bcrypt.CompareHashAndPassword([]byte(u.PasswordHash), []byte("original-password")) != nil {
		t.Fatal("identity or budget lost", err)
	}
	authenticated, keyID, err := s.AuthenticateAPIKey(ctx, keys[0].Key)
	if err != nil || authenticated.ID != u.ID {
		t.Fatal("legacy key did not authenticate", err)
	}
	token, err := s.RevealAPIKey(ctx, u.ID, keyID, master)
	if err != nil || token != keys[0].Key {
		t.Fatal("legacy key encryption/reveal", err)
	}
	for _, i := range []int{1, 2, 3, 4} {
		if _, _, err = s.AuthenticateAPIKey(ctx, keys[i].Key); err == nil {
			t.Fatal("restricted, disabled or skipped key authenticated", i)
		}
	}
	encoded, _ := json.Marshal(out)
	if strings.Contains(string(encoded), keys[0].Key) || strings.Contains(string(encoded), string(hash)) {
		t.Fatal("result leaked secrets")
	}
	s.DB.Exec("UPDATE users SET limit_5h=23,limit_7d=45 WHERE id=?", u.ID)
	if err = s.RevokeAPIKey(ctx, u.ID, keyID); err != nil {
		t.Fatal(err)
	}
	// Retrying with acceptance cannot re-enable an existing revoked/restricted key.
	if _, err = s.ImportSub2APIUsers(ctx, admin, "old-site", users, keys, master, false, true); err != nil {
		t.Fatal(err)
	}
	u, _ = s.UserByName(ctx, u.Username)
	if u.Limit5h != 23 || u.Limit7d != 45 {
		t.Fatal("retry reset budgets")
	}
	if _, _, err = s.AuthenticateAPIKey(ctx, keys[0].Key); err == nil {
		t.Fatal("retry re-enabled revoked key")
	}
	if _, _, err = s.AuthenticateAPIKey(ctx, keys[1].Key); err == nil {
		t.Fatal("retry re-enabled restricted key")
	}
	// Another source cannot claim the same email and attach its own key.
	conflict := []MigrationKey{{ID: 9, UserID: 1, Name: "conflict", Key: "legacy-conflicting-key-fixture", Status: "active"}}
	retry, err := s.ImportSub2APIUsers(ctx, admin, "different-site", users[:1], conflict, master, false, true)
	if err != nil || retry[1].Status != "skipped" {
		t.Fatal("cross-source ownership collision", err)
	}
}

func TestSub2APIKeyPolicyAcceptanceAndRollback(t *testing.T) {
	ctx := context.Background()
	s, err := Open(ctx, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	admin, _ := s.CreateAdmin(ctx, "admin", "hash")
	hash, _ := bcrypt.GenerateFromPassword([]byte("original-password"), bcrypt.MinCost)
	users := []MigrationUser{{ID: 1, Email: "member@example.com", PasswordHash: string(hash), Status: "active"}}
	group := int64(3)
	future := time.Now().Add(time.Hour)
	keys := []MigrationKey{{ID: 1, UserID: 1, Name: "policy", Key: "legacy-policy-key-fixture", Status: "active", GroupID: &group, ExpiresAt: &future}, {ID: 2, UserID: 1, Name: "disabled", Key: "legacy-disabled-key-fixture", Status: "disabled"}}
	if _, err = s.ImportSub2APIUsers(ctx, admin, "site", users, keys, nil, false, true); err == nil {
		t.Fatal("invalid master key accepted")
	}
	if _, err = s.UserByName(ctx, "member@example.com"); err == nil {
		t.Fatal("partial user committed after key failure")
	}
	if _, err = s.ImportSub2APIUsers(ctx, admin, "site", users, keys, make([]byte, 32), false, true); err != nil {
		t.Fatal(err)
	}
	if _, _, err = s.AuthenticateAPIKey(ctx, keys[0].Key); err != nil {
		t.Fatal("explicit policy conversion failed", err)
	}
	if _, _, err = s.AuthenticateAPIKey(ctx, keys[1].Key); err == nil {
		t.Fatal("disabled key enabled")
	}
	for _, token := range []string{"", strings.Repeat("x", 129), "legacy-key-with\nnewline", strings.Repeat(" ", 20)} {
		if ValidAPIKeyToken(token) {
			t.Fatal("invalid token accepted")
		}
	}
}

func TestSub2APIRetryReportsChangedPasswordWithoutOverwriting(t *testing.T) {
	ctx := context.Background()
	s, err := Open(ctx, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	actor, _ := s.CreateAdmin(ctx, "admin", "hash")
	original, _ := bcrypt.GenerateFromPassword([]byte("source-original-password"), bcrypt.MinCost)
	users := []MigrationUser{{ID: 1, Email: "member@example.com", PasswordHash: string(original), Status: "active"}}
	if _, err = s.ImportSub2APIUsers(ctx, actor, "source", users, nil, make([]byte, 32), false, false); err != nil {
		t.Fatal(err)
	}
	member, err := s.UserByName(ctx, users[0].Email)
	if err != nil || member.PasswordHash != string(original) {
		t.Fatal("source password hash was changed during import")
	}
	changed, _ := bcrypt.GenerateFromPassword([]byte("new-local-password"), bcrypt.MinCost)
	if err = s.ChangePassword(ctx, actor, member.ID, string(changed), false); err != nil {
		t.Fatal(err)
	}
	for _, preview := range []bool{true, false} {
		result, err := s.ImportSub2APIUsers(ctx, actor, "source", users, nil, make([]byte, 32), preview, false)
		if err != nil || len(result) != 1 || !strings.Contains(result[0].Warning, "密码记录与本次导出不同") {
			t.Fatal("password difference was silently skipped", err)
		}
		saved, err := s.UserByName(ctx, users[0].Email)
		if err != nil || saved.PasswordHash != string(changed) || bcrypt.CompareHashAndPassword([]byte(saved.PasswordHash), []byte("new-local-password")) != nil {
			t.Fatal("retry overwrote the local password")
		}
	}
}

func TestSub2APIRejectsCorruptedPasswordWrite(t *testing.T) {
	ctx := context.Background()
	s, err := Open(ctx, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	actor, _ := s.CreateAdmin(ctx, "admin", "hash")
	if _, err = s.DB.Exec("CREATE TRIGGER test_corrupt_password AFTER INSERT ON users WHEN NEW.role='user' BEGIN UPDATE users SET password_hash='corrupted-fixture' WHERE id=NEW.id; END"); err != nil {
		t.Fatal(err)
	}
	hash, _ := bcrypt.GenerateFromPassword([]byte("source-password-fixture"), bcrypt.MinCost)
	users := []MigrationUser{{ID: 1, Email: "member@example.com", PasswordHash: string(hash), Status: "active"}}
	if _, err = s.ImportSub2APIUsers(ctx, actor, "source", users, nil, make([]byte, 32), false, false); err == nil {
		t.Fatal("password corruption committed")
	}
	found, _ := s.Users(ctx)
	if len(found) != 1 {
		t.Fatal("failed import left a partial user")
	}
}
