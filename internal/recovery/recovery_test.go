package recovery_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fiftyapi/internal/accounts"
	"fiftyapi/internal/providers"
	"fiftyapi/internal/store"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestLegacyNamedBackupStillRestores(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("Python required")
	}
	dir := t.TempDir()
	source := filepath.Join(dir, "snapshot")
	if err = os.Mkdir(source, 0700); err != nil {
		t.Fatal(err)
	}
	data := []byte("legacy snapshot bytes")
	if err = os.WriteFile(filepath.Join(source, "ztokenmgr.db"), data, 0600); err != nil {
		t.Fatal(err)
	}
	hash := sha256.Sum256(data)
	manifest, _ := json.Marshal(map[string]any{"format": 1, "files": map[string]string{"ztokenmgr.db": hex.EncodeToString(hash[:])}})
	if err = os.WriteFile(filepath.Join(source, "manifest.json"), manifest, 0600); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "restored")
	command := exec.Command(python, filepath.Join("..", "..", "scripts", "backup-offline.py"), "restore", "--from", source, "--output", out)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("legacy restore failed: %v %s", err, output)
	}
	got, err := os.ReadFile(filepath.Join(out, "ztokenmgr.db"))
	if err != nil || !bytes.Equal(got, data) {
		t.Fatal("legacy backup changed or disappeared", err)
	}
}

func TestOfflineBackupRestorePreservesEncryptedAccountsAndLedger(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("Python 3 is required for offline backup tool integration")
	}
	script := filepath.Join("..", "..", "scripts", "backup-offline.py")
	dir := t.TempDir()
	path := filepath.Join(dir, "source", "app.db")
	ctx := context.Background()
	s, err := store.Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	user, err := s.CreateAdmin(ctx, "admin", "retained-password-hash")
	if err != nil {
		t.Fatal(err)
	}
	if err = s.SetLimits(ctx, user, user, 100000, 500000); err != nil {
		t.Fatal(err)
	}
	key, token, err := s.CreateAPIKey(ctx, user, "restored-key")
	if err != nil {
		t.Fatal(err)
	}
	master := make([]byte, 32)
	rand.Read(master)
	service, err := accounts.New(s, master)
	if err != nil {
		t.Fatal(err)
	}
	cfg := providers.Config{Provider: "antigravity", ClientID: "test", Scope: "scope", AuthorizeURL: "https://auth.example/authorize", TokenURL: "https://auth.example/token", RedirectURI: "http://localhost/callback", TokenEncoding: "form", ModelsURL: "https://upstream.example/models"}
	id, err := service.Import(ctx, user, "backup-account", accounts.Credential{Config: cfg, Token: providers.Token{AccessToken: "fake-backup-access", RefreshToken: "fake-backup-refresh"}, ExpiresAt: time.Now().Add(time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.SyncModels(ctx, id, "antigravity", []string{"model"}); err != nil {
		t.Fatal(err)
	}
	if err = s.Reserve(ctx, "settled", user, key.ID, "model", store.Price{Input: 1000000}, 100, time.Now()); err != nil {
		t.Fatal(err)
	}
	if err = s.Settle(ctx, "settled", 30, time.Now()); err != nil {
		t.Fatal(err)
	}
	if err = s.Reserve(ctx, "interrupted", user, key.ID, "model", store.Price{Input: 1000000}, 70, time.Now()); err != nil {
		t.Fatal(err)
	}
	run := func(args ...string) error { return exec.Command(python, append([]string{script}, args...)...).Run() }
	backup := filepath.Join(dir, "backup")
	unlock, err := s.LockServer()
	if err != nil {
		t.Fatal(err)
	}
	if run("create", "--db", path, "--output", backup) == nil {
		unlock()
		t.Fatal("backup permitted while server lock held")
	}
	unlock()
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	if err = run("create", "--db", path, "--output", backup); err != nil {
		t.Fatal("offline backup failed")
	}
	if run("create", "--db", path, "--output", backup) == nil {
		t.Fatal("backup overwrote existing snapshot")
	}
	restoredDir := filepath.Join(dir, "restored")
	if err = run("restore", "--from", backup, "--output", restoredDir); err != nil {
		t.Fatal("restore failed")
	}
	if run("restore", "--from", backup, "--output", restoredDir) == nil {
		t.Fatal("restore overwrote existing data")
	}
	restored, err := store.Open(ctx, filepath.Join(restoredDir, "fiftyapi.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	if _, _, err = restored.AuthenticateAPIKey(ctx, token); err != nil {
		t.Fatal("API key ownership/hash not restored")
	}
	_, err = accounts.New(restored, master)
	if err != nil {
		t.Fatal("original master key rejected")
	}
	row, err := restored.Account(ctx, id)
	if err != nil || bytes.Contains(row.Credentials, []byte("fake-backup-access")) {
		t.Fatal("encrypted credentials lost or plaintext persisted")
	}
	wrong := append([]byte(nil), master...)
	wrong[0] ^= 1
	if _, err = accounts.New(restored, wrong); err == nil {
		t.Fatal("wrong master key accepted after restore")
	}
	if err = restored.RecoverInFlight(ctx); err != nil {
		t.Fatal(err)
	}
	windows, err := restored.Quota(ctx, user)
	if err != nil {
		t.Fatal(err)
	}
	for _, window := range windows {
		if window.Used != 30 || window.Reserved != 0 {
			t.Fatal("recovery failed to preserve used quota and release unknown reservation")
		}
	}
	pending, err := restored.PendingCharges(ctx)
	if err != nil || len(pending) != 0 {
		t.Fatal("interrupted unknown request still pending after recovery")
	}
	// Corruption must be detected before creating a restore target.
	f, err := os.OpenFile(filepath.Join(backup, "fiftyapi.db"), os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	f.Write([]byte("corruption"))
	f.Close()
	corruptTarget := filepath.Join(dir, "corrupt-restore")
	if run("restore", "--from", backup, "--output", corruptTarget) == nil {
		t.Fatal("corrupt backup restored")
	}
	if _, err = os.Stat(corruptTarget); !os.IsNotExist(err) {
		t.Fatal("failed restore left partial target")
	}
	t.Log("offline restore passed: live-lock refusal, no overwrite, checksums, encrypted account/master-key check, API key, both quota windows and crash recovery")
}
