package server

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fiftyapi/internal/accounts"
	"fiftyapi/internal/store"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

// Opt-in local acceptance: never copy real credentials into fixtures or contact
// upstreams. Only a temporary SQLite database is written; source is unchanged.
func TestSub2APIProvidedBundleOffline(t *testing.T) {
	path := os.Getenv("FIFTYAPI_SUB2API_TEST_BUNDLE")
	if path == "" {
		t.Skip("local bundle not configured")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal("cannot read provided bundle")
	}
	var bundle sub2apiBundle
	if json.Unmarshal(raw, &bundle) != nil {
		t.Fatal("invalid bundle JSON")
	}
	canonical, _ := json.Marshal(bundle)
	t.Logf("Provided bundle data checksum: %x", sha256.Sum256(canonical))
	ctx := context.Background()
	s, err := store.Open(ctx, filepath.Join(t.TempDir(), "offline.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	actor, err := s.CreateAdminWithMigration(ctx, "isolated-test-admin", "hash", true)
	if err != nil {
		t.Fatal(err)
	}
	master := make([]byte, 32)
	api, _ := New(s, Config{SettingsKey: master})
	svc, _ := accounts.New(s, master)
	api.AttachAccounts(svc)
	var admin store.User
	admin.ID = actor
	run := func(preview bool) []store.MigrationItem {
		body, _ := json.Marshal(map[string]any{"data": bundle, "preview": preview, "accept_key_policy": true})
		r := httptest.NewRequest("POST", "http://example.test/api/admin/migrations/sub2api", bytes.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		api.migrateSub2API(w, r, identity{user: admin})
		if w.Code != http.StatusOK {
			t.Fatalf("bundle migration HTTP %d (response withheld)", w.Code)
		}
		var result struct {
			Items []store.MigrationItem `json:"items"`
		}
		if json.Unmarshal(w.Body.Bytes(), &result) != nil {
			t.Fatal("invalid migration response")
		}
		for _, key := range bundle.Keys {
			if key.Key != "" && bytes.Contains(w.Body.Bytes(), []byte(key.Key)) {
				t.Fatal("response exposed a source key")
			}
		}
		for _, user := range bundle.Users {
			if user.PasswordHash != "" && bytes.Contains(w.Body.Bytes(), []byte(user.PasswordHash)) {
				t.Fatal("response exposed a password hash")
			}
		}
		return result.Items
	}
	preview := run(true)
	users, _ := s.Users(ctx)
	rows, _ := s.Accounts(ctx)
	if len(users) != 1 || len(rows) != 0 {
		t.Fatal("preview saved source data")
	}
	imported := run(false)
	counts := map[string]int{}
	skipped := map[string]int{}
	for _, item := range imported {
		if item.Status == "imported" {
			counts[item.Kind]++
		} else {
			skipped[item.Kind]++
		}
	}
	t.Logf("Provided bundle: users=%d keys=%d accounts=%d proxies=%d; imported users=%d keys=%d accounts=%d; skipped=%d", len(bundle.Users), len(bundle.Keys), len(bundle.Accounts), len(bundle.Proxies), counts["user"], counts["key"], counts["account"], len(imported)-counts["user"]-counts["key"]-counts["account"])
	if len(preview) != len(imported) || counts["user"] != len(bundle.Users) || counts["key"] != len(bundle.Keys) || counts["account"] != len(bundle.Accounts) {
		t.Fatal("provided bundle has unexpected skipped rows (details withheld)")
	}
	for _, u := range bundle.Users {
		saved, err := s.UserByName(ctx, u.Email)
		if err != nil || saved.PasswordHash != u.PasswordHash || saved.Limit5h != store.DefaultMemberBudget || saved.Limit7d != store.DefaultMemberBudget {
			t.Fatal("identity or defaults changed")
		}
	}
	retry := run(false)
	for _, item := range retry {
		if item.Status != "skipped" {
			t.Fatal("retry duplicated source data")
		}
	}
	after, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(raw, after) {
		t.Fatal("source file changed")
	}
}
