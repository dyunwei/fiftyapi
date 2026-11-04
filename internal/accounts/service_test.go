package accounts

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"fiftyapi/internal/credentials"
	"fiftyapi/internal/providers"
	"fiftyapi/internal/store"
)

func TestRefreshSavedBeforeLaterFailure(t *testing.T) {
	ctx := context.Background()
	s, err := store.Open(ctx, filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	actor, _ := s.CreateAdmin(ctx, "admin", "hash")
	key := make([]byte, 32)
	service, err := New(s, key)
	if err != nil {
		t.Fatal(err)
	}
	var refreshes atomic.Int32
	var failModels atomic.Bool
	failModels.Store(true)
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/token":
			refreshes.Add(1)
			r.ParseForm()
			if r.Form.Get("refresh_token") != "private-refresh" || r.Form.Get("client_secret") != "client-secret" {
				t.Error("incorrect refresh parameters")
			}
			io.WriteString(w, `{"access_token":"new-access","refresh_token":"rotated-refresh","expires_in":3600}`)
		case "/v1internal:loadCodeAssist":
			io.WriteString(w, `{"cloudaicompanionProject":"existing"}`)
		case "/v1internal:fetchAvailableModels":
			if r.Header.Get("Authorization") != "Bearer new-access" {
				t.Error("stale bearer")
			}
			if failModels.Load() {
				w.WriteHeader(500)
				io.WriteString(w, `{"error":"private-message"}`)
			} else {
				io.WriteString(w, `{"models":{"gemini-3.5-flash-lite":{}}}`)
			}
		default:
			t.Error("unexpected upstream path")
			w.WriteHeader(404)
		}
	}))
	defer upstream.Close()
	service.client = upstream.Client()
	t.Setenv("ANTIGRAVITY_OAUTH_CLIENT_SECRET", "client-secret")
	cfg := providers.Config{Provider: "antigravity", ClientID: "client", Scope: "scope", RedirectURI: "http://localhost:8085/callback", AuthorizeURL: upstream.URL + "/authorize", TokenURL: upstream.URL + "/token", TokenEncoding: "form", ModelsURL: upstream.URL + "/v1internal:fetchAvailableModels", ModelsMethod: "POST"}
	id, err := service.Import(ctx, actor, "account", Credential{Config: cfg, Token: providers.Token{AccessToken: "private-access", RefreshToken: "private-refresh"}, ExpiresAt: time.Now().Add(-time.Minute)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = service.Verify(ctx, id); err == nil {
		t.Fatal("accepted failing upstream")
	}
	row, _ := s.Account(ctx, id)
	if row.State != "error" || strings.Contains(string(row.Credentials), "private") || strings.Contains(row.LastError, "private-message") {
		t.Fatal("state or encryption failure")
	}
	plain, err := credentials.Decrypt(key, binding(id), row.Credentials)
	if err != nil {
		t.Fatal(err)
	}
	var stored Credential
	json.Unmarshal(plain, &stored)
	if stored.Token.RefreshToken != "rotated-refresh" {
		t.Fatal("refresh token lost after model failure")
	}
	failModels.Store(false)
	var wg sync.WaitGroup
	errs := make(chan error, 5)
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); _, err := service.Verify(ctx, id); errs <- err }()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	row, _ = s.Account(ctx, id)
	if refreshes.Load() != 1 || row.State != "catalog_ready" || row.LastError != "" {
		t.Fatal("unnecessary refresh or wrong account state")
	}
	if _, err = New(s, []byte(strings.Repeat("x", 32))); err == nil {
		t.Fatal("accepted wrong master key")
	}
	if err = s.EnableAccount(ctx, actor, id, false); err != nil {
		t.Fatal(err)
	}
	if _, err = service.Verify(ctx, id); err == nil {
		t.Fatal("used disabled account")
	}
}

func TestRefreshRevocationState(t *testing.T) {
	ctx := context.Background()
	s, err := store.Open(ctx, filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	actor, _ := s.CreateAdmin(ctx, "admin", "hash")
	service, err := New(s, make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(400)
		io.WriteString(w, `{"error":"invalid_grant","error_description":"private-token"}`)
	}))
	defer upstream.Close()
	service.client = upstream.Client()
	t.Setenv("ANTIGRAVITY_OAUTH_CLIENT_SECRET", "client-secret")
	cfg := providers.Config{Provider: "antigravity", ClientID: "client", Scope: "scope", RedirectURI: "http://localhost/callback", AuthorizeURL: upstream.URL, TokenURL: upstream.URL, TokenEncoding: "form", ModelsURL: upstream.URL}
	id, err := service.Import(ctx, actor, "revoked", Credential{Config: cfg, Token: providers.Token{AccessToken: "access", RefreshToken: "refresh"}, ExpiresAt: time.Now().Add(-time.Minute)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = service.Verify(ctx, id); err == nil {
		t.Fatal("accepted revoked grant")
	}
	row, _ := s.Account(ctx, id)
	if row.State != "reauth_required" || strings.Contains(row.LastError, "private-token") {
		t.Fatal("revocation state or redaction wrong")
	}
}
