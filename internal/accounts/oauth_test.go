package accounts

import (
	"context"
	"encoding/json"
	"fiftyapi/internal/providers"
	"fiftyapi/internal/store"
	"path/filepath"
	"testing"
	"time"
)

func TestOAuthOwnerAndOneTimeValidation(t *testing.T) {
	ctx := context.Background()
	s, e := store.Open(ctx, filepath.Join(t.TempDir(), "db"))
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	service, e := New(s, make([]byte, 32))
	if e != nil {
		t.Fatal(e)
	}
	started, e := service.StartOAuth(ctx, 1, "antigravity", "test", 0)
	if e != nil {
		t.Fatal(e)
	}
	if _, _, e = service.CompleteOAuth(ctx, 2, started.SessionID, "wrong#state", false); e == nil {
		t.Fatal("other admin consumed session")
	}
	service.oauthMu.Lock()
	_, present := service.oauth[started.SessionID]
	service.oauthMu.Unlock()
	if !present {
		t.Fatal("wrong owner deleted session")
	}
	if _, _, e = service.CompleteOAuth(ctx, 1, started.SessionID, "wrong#state", false); e == nil {
		t.Fatal("invalid state accepted")
	}
	if _, _, e = service.CompleteOAuth(ctx, 1, started.SessionID, "wrong#state", false); e == nil {
		t.Fatal("replayed session accepted")
	}
}
func TestRuntimeConfigMasksProxyAndRejectsCredentialHeaders(t *testing.T) {
	ctx := context.Background()
	s, e := store.Open(ctx, filepath.Join(t.TempDir(), "db"))
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	actor, _ := s.CreateAdmin(ctx, "admin", "hash")
	service, e := New(s, make([]byte, 32))
	if e != nil {
		t.Fatal(e)
	}
	c := testCredential()
	c.Config.ProxyURL = "https://operator:private-password@proxy.example:443"
	id, e := service.Import(ctx, actor, "account", c)
	if e != nil {
		t.Fatal(e)
	}
	config, e := service.RuntimeConfig(ctx, id)
	if e != nil {
		t.Fatal(e)
	}
	raw, _ := json.Marshal(config)
	if string(raw) == "" || config.ProxyURL == c.Config.ProxyURL {
		t.Fatal("proxy password exposed")
	}
	if e = service.UpdateRuntime(ctx, actor, id, config); e != nil {
		t.Fatal(e)
	}
	config.GatewayHeaders = map[string]string{"Authorization": "Bearer hidden"}
	if e = service.UpdateRuntime(ctx, actor, id, config); e == nil {
		t.Fatal("credential header accepted")
	}
}

func testCredential() Credential {
	return Credential{Config: providers.Config{Provider: "antigravity", ClientID: "client", Scope: "scope", AuthorizeURL: "https://auth.example/authorize", TokenURL: "https://auth.example/token", RedirectURI: "http://localhost/callback", TokenEncoding: "form", ModelsURL: "https://upstream.example/models"}, Token: providers.Token{AccessToken: "fake-access"}, ExpiresAt: time.Now().Add(time.Hour)}
}
