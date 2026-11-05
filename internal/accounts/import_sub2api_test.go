package accounts

import (
	"context"
	"encoding/json"
	"fiftyapi/internal/credentials"
	"fiftyapi/internal/store"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSub2APIAccountConversionAndIdempotency(t *testing.T) {
	ctx := context.Background()
	db, err := store.Open(ctx, filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	actor, _ := db.CreateAdmin(ctx, "admin", "hash")
	master := make([]byte, 32)
	service, err := New(db, master)
	if err != nil {
		t.Fatal(err)
	}
	proxyID := int64(1)
	proxy := Sub2APIProxy{ID: 1, Protocol: "socks5", Host: "proxy.example", Port: 1080, Username: "user", Password: "p@ss", Status: "active"}
	auth, _ := json.Marshal(map[string]any{"access_token": "access-fixture", "refresh_token": "refresh-fixture", "expires_at": time.Now().Add(time.Hour).Unix(), "project_id": "old-project"})
	entries := []Sub2APIAccount{{ID: 1, Name: "Antigravity old", Platform: "antigravity", Type: "oauth", Status: "active", Credentials: auth, ProxyID: &proxyID, Concurrency: 8, Priority: 12}, {ID: 2, Name: "unsupported", Platform: "gemini", Type: "oauth", Status: "active", Credentials: auth}, {ID: 3, Name: "API old", Platform: "anthropic", Type: "apikey", Status: "disabled", Credentials: json.RawMessage(`{"api_key":"private-upstream-fixture","base_url":"https://upstream.example","model_mapping":{"alias-model":"target-model"}}`)}}
	preview, err := service.ImportSub2API(ctx, actor, entries, []Sub2APIProxy{proxy}, true)
	if err != nil {
		t.Fatal(err)
	}
	rows, _ := db.Accounts(ctx)
	if len(rows) != 0 {
		t.Fatal("preview saved accounts")
	}
	if preview[0].Status != "imported" || preview[1].Status != "skipped" || preview[2].Enabled {
		t.Fatalf("preview: %+v", preview)
	}
	saved, err := service.ImportSub2API(ctx, actor, entries, []Sub2APIProxy{proxy}, false)
	if err != nil {
		t.Fatal(err)
	}
	row, _ := db.Account(ctx, saved[0].ID)
	plain, err := credentials.Decrypt(master, binding(row.ID), row.Credentials)
	if err != nil {
		t.Fatal(err)
	}
	var c Credential
	json.Unmarshal(plain, &c)
	if !strings.Contains(string(c.Config.ModelsBody), "old-project") || c.Config.ProxyURL != "socks5://user:p%40ss@proxy.example:1080" {
		t.Fatal("lost project or proxy")
	}
	policy, _ := db.AccountPolicy(ctx, row.ID)
	if policy.Concurrency != 8 || policy.Priority != 12 {
		t.Fatal("lost scheduling policy", policy)
	}
	row, _ = db.Account(ctx, saved[2].ID)
	if row.Enabled {
		t.Fatal("disabled upstream enabled")
	}
	plain, _ = credentials.Decrypt(master, binding(row.ID), row.Credentials)
	json.Unmarshal(plain, &c)
	if c.Config.Gateway == nil || c.Config.Gateway.Discovery != "manual" || c.Config.Gateway.Models[0].ID != "alias-model" {
		t.Fatal("API Key mapping lost")
	}
	again, err := service.ImportSub2API(ctx, actor, entries, []Sub2APIProxy{proxy}, false)
	if err != nil || again[0].Status != "skipped" || again[2].Status != "skipped" {
		t.Fatal("retry created duplicates", err)
	}
	entries[0].Name = "missing proxy"
	entries[0].ID = 4
	missing, _ := service.ImportSub2API(ctx, actor, entries[:1], nil, true)
	if missing[0].Status != "skipped" {
		t.Fatal("missing proxy silently became direct")
	}
}
