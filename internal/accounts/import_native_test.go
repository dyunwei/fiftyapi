package accounts

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"fiftyapi/internal/store"
)

func TestNativeClaudeCredentialImport(t *testing.T) {
	ctx := context.Background()
	db, err := store.Open(ctx, filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	actor, err := db.CreateAdmin(ctx, "admin", "hash")
	if err != nil {
		t.Fatal(err)
	}
	service, err := New(db, make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	expiry := time.Now().Add(time.Hour).Truncate(time.Millisecond)
	raw, err := json.Marshal(map[string]any{"claudeAiOauth": map[string]any{"accessToken": "native-access-fixture", "refreshToken": "native-refresh-fixture", "expiresAt": expiry.UnixMilli()}, "config": map[string]any{"proxy_url": "https://untrusted.example", "max_concurrency": 99}})
	if err != nil {
		t.Fatal(err)
	}
	id, err := service.ImportAuth(ctx, actor, "native", "claude", raw)
	if err != nil {
		t.Fatal(err)
	}
	saved, err := service.Access(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if saved.Token.AccessToken != "native-access-fixture" || saved.Token.RefreshToken != "native-refresh-fixture" || saved.ExpiresAt.Unix() != expiry.Unix() {
		t.Fatal("native OAuth fields or milliseconds lost")
	}
	if saved.Config.ProxyURL != "" || saved.Config.MaxConcurrency == 99 {
		t.Fatal("imported untrusted runtime config")
	}
	if _, err = service.ImportAuth(ctx, actor, "wrong-provider", "openai", raw); err == nil {
		t.Fatal("Claude native file accepted for another provider")
	}
}
