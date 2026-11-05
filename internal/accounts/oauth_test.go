package accounts

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fiftyapi/internal/providers"
	"fiftyapi/internal/store"
	"io"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
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

// Exercise the real exchange and persistence path without contacting a provider.
func TestContributedOAuthScopeAndPersistence(t *testing.T) {
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
	calls := 0
	service.client = &http.Client{Transport: contributionTokenTransport(func(r *http.Request) (*http.Response, error) {
		calls++
		if !strings.Contains(r.URL.Path, "token") {
			t.Fatalf("unexpected verification request: %s", r.URL.Path)
		}
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"access_token":"contributed-access","refresh_token":"contributed-refresh","expires_in":3600}`))}, nil
	})}
	for _, provider := range []string{"openai", "claude", "antigravity"} {
		t.Run(provider, func(t *testing.T) {
			hash := sha256.Sum256([]byte(provider))
			link, err := db.CreateContributionLink(ctx, actor, hash[:], provider, "", time.Now().Add(time.Hour))
			if err != nil {
				t.Fatal(err)
			}
			start := func(name string) (OAuthStart, string) {
				t.Helper()
				started, err := service.StartContributedOAuth(ctx, actor, provider, name, link)
				if err != nil {
					t.Fatal(err)
				}
				u, _ := url.Parse(started.AuthURL)
				return started, "test-code#" + u.Query().Get("state")
			}
			started, result := start(provider)
			if _, _, err = service.CompleteOAuth(ctx, actor, started.SessionID, result, false); err == nil {
				t.Fatal("admin endpoint accepted contribution session")
			}
			if _, err = service.CompleteContributedOAuth(ctx, actor, started.SessionID, result, link+1); err == nil {
				t.Fatal("other link accepted session")
			}
			id, err := service.CompleteContributedOAuth(ctx, actor, started.SessionID, result, link)
			if err != nil {
				t.Fatal(err)
			}
			row, err := db.Account(ctx, id)
			if err != nil || row.Enabled {
				t.Fatalf("contribution account enabled or missing: %v", err)
			}
			var savedLink int64
			if err = db.DB.QueryRowContext(ctx, "SELECT contribution_link_id FROM upstream_accounts WHERE id=?", id).Scan(&savedLink); err != nil || savedLink != link {
				t.Fatalf("lost contribution origin: %v", err)
			}
			if bytes.Contains(row.Credentials, []byte("contributed-access")) {
				t.Fatal("credentials stored in plaintext")
			}
			before := calls
			if _, err = service.CompleteContributedOAuth(ctx, actor, started.SessionID, result, link); err == nil || calls != before {
				t.Fatal("replayed session reached upstream")
			}
			started, result = start(provider + "-duplicate")
			if _, err = service.CompleteContributedOAuth(ctx, actor, started.SessionID, result, link); err == nil {
				t.Fatal("duplicate credential accepted")
			}
			started, result = start(provider + "-revoked")
			if err = db.RevokeContributionLink(ctx, actor, link); err != nil {
				t.Fatal(err)
			}
			if _, err = service.CompleteContributedOAuth(ctx, actor, started.SessionID, result, link); err == nil {
				t.Fatal("revoked link saved an account")
			}
			admin, err := service.StartOAuth(ctx, actor, provider, provider+"-admin", 0)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = service.CompleteContributedOAuth(ctx, actor, admin.SessionID, result, link); err == nil {
				t.Fatal("contribution endpoint accepted admin session")
			}
		})
	}
}

type contributionTokenTransport func(*http.Request) (*http.Response, error)

func (f contributionTokenTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
