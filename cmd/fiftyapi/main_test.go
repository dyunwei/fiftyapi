package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"fiftyapi/internal/accounts"
	"fiftyapi/internal/credentials"
	"fiftyapi/internal/providers"
	"fiftyapi/internal/store"
)

func TestImportAccountUsesExistingEncryptedSession(t *testing.T) {
	dir := t.TempDir()
	ctx := context.Background()
	s, err := store.Open(ctx, filepath.Join(dir, "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	s.CreateAdmin(ctx, "admin", "hash")
	service, err := accounts.New(s, make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	config := providers.Config{Provider: "antigravity", ClientID: "client", Scope: "scope", RedirectURI: "http://localhost/callback", AuthorizeURL: "https://auth.example/authorize", TokenURL: "https://auth.example/token", TokenEncoding: "form", ModelsURL: "https://upstream.example/models"}
	configPath := filepath.Join(dir, "config.json")
	cfg, _ := json.Marshal(config)
	os.WriteFile(configPath, cfg, 0600)
	keyPath := filepath.Join(dir, "probe.key")
	key, err := credentials.LoadKey(keyPath)
	if err != nil {
		t.Fatal(err)
	}
	binding, _ := json.Marshal([]string{config.Provider, config.ClientID, config.AuthorizeURL, config.TokenURL, config.RedirectURI, config.Scope, config.ModelsURL})
	session := filepath.Join(dir, "probe.session")
	plain, _ := json.Marshal(map[string]any{"token": providers.Token{AccessToken: "fake-access", RefreshToken: "fake-refresh"}, "expires_at": time.Now().Add(time.Hour)})
	if err = credentials.Save(session, key, string(binding), plain); err != nil {
		t.Fatal(err)
	}
	args := []string{"--name", "test-account", "--admin", "admin", "--config", configPath, "--session-file", session, "--key-file", keyPath}
	if err = importAccount(ctx, args, s, service); err != nil {
		t.Fatal(err)
	}
	rows, err := s.Accounts(ctx)
	if err != nil || len(rows) != 1 || rows[0].State != "authorized" {
		t.Fatalf("account not imported: %v", err)
	}
	if err = importAccount(ctx, args, s, service); err == nil {
		t.Fatal("duplicate name imported")
	}
	rows, _ = s.Accounts(ctx)
	if len(rows) != 1 {
		t.Fatal("failed import left partial row")
	}
}

func TestServeLocksBeforeDatabaseCreationOrMigration(t *testing.T) {
	path := filepath.Join(t.TempDir(), "db")
	unlock, err := store.LockServerPath(path)
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	t.Setenv("FIFTYAPI_DB_PATH", path)
	t.Setenv("FIFTYAPI_MASTER_KEY", base64.StdEncoding.EncodeToString(make([]byte, 32)))
	previous := os.Args
	os.Args = []string{"fiftyapi", "serve"}
	defer func() { os.Args = previous }()
	if err = run(); err == nil || !strings.Contains(err.Error(), "已有服务实例") {
		t.Fatal("second server did not fail at instance lock")
	}
	if _, err = os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("second server opened the database before acquiring the lock")
	}
}

func TestDatabasePathPreservesLegacyAndRejectsAmbiguity(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("FIFTYAPI_DATA_DIR", dir)
	t.Setenv("FIFTYAPI_DB_PATH", "")
	current, legacy := filepath.Join(dir, "fiftyapi.db"), filepath.Join(dir, "ztokenmgr.db")
	if path, err := databasePath(); err != nil || path != current {
		t.Fatal("new database default", path, err)
	}
	if err := os.WriteFile(legacy, []byte("legacy-placeholder"), 0600); err != nil {
		t.Fatal(err)
	}
	if path, err := databasePath(); err != nil || path != legacy {
		t.Fatal("legacy database not selected", path, err)
	}
	if err := os.WriteFile(current, []byte("current-placeholder"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := databasePath(); err == nil {
		t.Fatal("ambiguous database auto-selected")
	}
	t.Setenv("FIFTYAPI_DB_PATH", legacy)
	if path, err := databasePath(); err != nil || path != legacy {
		t.Fatal("explicit choice ignored", path, err)
	}
	t.Setenv("FIFTYAPI_DB_PATH", "")
	os.Unsetenv("FIFTYAPI_DB_PATH")
	t.Setenv("ZTOKENMGR_DB_PATH", legacy)
	if path, err := databasePath(); err != nil || path != legacy {
		t.Fatal("old deployment path ignored", path, err)
	}
}
