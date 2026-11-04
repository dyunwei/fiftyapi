package providers

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"
)

func config() Config {
	return Config{Provider: "test", AuthorizeURL: "https://auth.example/authorize", TokenURL: "https://auth.example/token", ClientID: "test", RedirectURI: "http://localhost:1455/auth/callback", Scope: "openid offline_access", TokenEncoding: "form", ModelsURL: "https://api.example/models"}
}
func result(c Config, auth string) string {
	u, _ := url.Parse(auth)
	return c.RedirectURI + "?" + url.Values{"state": {u.Query().Get("state")}, "code": {"private-code"}}.Encode()
}

func TestSessionValidation(t *testing.T) {
	c := config()
	s, a, err := Start(c)
	if err != nil {
		t.Fatal(err)
	}
	u, _ := url.Parse(a)
	if u.Query().Get("code_challenge_method") != "S256" || u.Query().Get("code_challenge") == "" {
		t.Fatal("missing PKCE")
	}
	if _, err = s.consume(c, c.RedirectURI+"?code=secret&state=wrong"); err == nil {
		t.Fatal("accepted wrong state")
	}
	if _, err = s.consume(c, strings.Replace(result(c, a), "localhost:1455", "evil.example", 1)); err == nil {
		t.Fatal("accepted wrong callback")
	}
	if _, err = s.consume(c, result(c, a)); err != nil {
		t.Fatal(err)
	}
	if _, err = s.consume(c, result(c, a)); err == nil {
		t.Fatal("accepted replay")
	}
	s, a, _ = Start(c)
	s.expires = time.Now().Add(-time.Second)
	if _, err = s.consume(c, result(c, a)); err == nil {
		t.Fatal("accepted expired session")
	}
}

func TestConcurrentConsumption(t *testing.T) {
	c := config()
	s, a, _ := Start(c)
	var wg sync.WaitGroup
	ok := make(chan bool, 20)
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); _, err := s.consume(c, result(c, a)); ok <- err == nil }()
	}
	wg.Wait()
	close(ok)
	n := 0
	for success := range ok {
		if success {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("consumed %d times", n)
	}
}

func TestExchangeRefreshModels(t *testing.T) {
	for _, encoding := range []string{"form", "json"} {
		t.Run(encoding, func(t *testing.T) {
			c := config()
			c.TokenEncoding = encoding
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				if r.URL.Path == "/models" {
					if r.Header.Get("Authorization") != "Bearer access" {
						t.Error("missing bearer")
					}
					io.WriteString(w, `{"models":[{"slug":"model-a"}]}`)
					return
				}
				values := map[string]string{}
				if encoding == "json" {
					if err := json.NewDecoder(r.Body).Decode(&values); err != nil {
						t.Error(err)
					}
				} else {
					r.ParseForm()
					for k := range r.PostForm {
						values[k] = r.PostForm.Get(k)
					}
				}
				if values["client_secret"] != "secret" {
					t.Error("missing secret")
				}
				switch values["grant_type"] {
				case "authorization_code":
					if values["code_verifier"] == "" || values["state"] == "" {
						t.Error("missing binding")
					}
					io.WriteString(w, `{"access_token":"access","refresh_token":"refresh","expires_in":3600}`)
				case "refresh_token":
					if values["refresh_token"] != "refresh" {
						t.Error("wrong refresh")
					}
					io.WriteString(w, `{"access_token":"access"}`)
				default:
					t.Error("wrong grant")
				}
			}))
			defer server.Close()
			c.AuthorizeURL = server.URL + "/authorize"
			c.TokenURL = server.URL + "/token"
			c.ModelsURL = server.URL + "/models"
			s, a, err := Start(c)
			if err != nil {
				t.Fatal(err)
			}
			tok, err := Exchange(context.Background(), server.Client(), c, s, result(c, a), "secret")
			if err != nil {
				t.Fatal(err)
			}
			tok, err = Refresh(context.Background(), server.Client(), c, tok, "secret")
			if err != nil || tok.RefreshToken != "refresh" {
				t.Fatalf("refresh failed: %v", err)
			}
			models, err := Models(context.Background(), server.Client(), c, tok)
			if err != nil || len(models) != 1 || models[0] != "model-a" {
				t.Fatalf("models failed: %v", err)
			}
		})
	}
}

func TestErrorsAreRedacted(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(401)
		io.WriteString(w, "private-token private-code")
	}))
	defer server.Close()
	c := config()
	c.TokenURL = server.URL
	_, err := tokenRequest(context.Background(), server.Client(), c, url.Values{}, "")
	if err == nil || strings.Contains(err.Error(), "private") {
		t.Fatalf("unsafe error: %v", err)
	}
}

func TestClientDoesNotFollowRedirect(t *testing.T) {
	reached := false
	dst := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { reached = true }))
	defer dst.Close()
	src := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, dst.URL, 307) }))
	defer src.Close()
	req, _ := http.NewRequest("GET", src.URL, nil)
	req.Header.Set("Authorization", "Bearer private")
	_, err := perform(NewClient(), req)
	if err == nil || reached {
		t.Fatal("redirect followed")
	}
}

func TestConfigRejectsOverrides(t *testing.T) {
	c := config()
	c.AuthParams = map[string]string{"state": "wrong"}
	if c.Validate() == nil {
		t.Fatal("state override")
	}
	c = config()
	c.ModelsHeaders = map[string]string{"authorization": "private"}
	if c.Validate() == nil {
		t.Fatal("credential header")
	}
	c = config()
	c.TokenURL = "http://example.com/token"
	if c.Validate() == nil {
		t.Fatal("plaintext upstream")
	}
}

func TestSafeErrorCodes(t *testing.T) {
	for _, tc := range []struct{ body, want string }{
		{`{"error":"invalid_client","error_description":"private-secret"}`, "invalid_client"},
		{`{"error":"invalid_grant"}`, "invalid_grant"},
		{`{"error":{"status":"UNAUTHENTICATED","message":"private-token"}}`, "UNAUTHENTICATED"},
		{`{"error":"private-token"}`, ""},
		{`{"error":{"status":"private-code"}}`, ""},
		{`invalid_client private-secret`, ""},
	} {
		if got := safeErrorCode([]byte(tc.body)); got != tc.want {
			t.Fatalf("got %q, want %q", got, tc.want)
		}
	}
}

func TestAntigravityInvalidClientExplainsOverrideWithoutLeaking(t *testing.T) {
	cfg, err := DefaultConfig("antigravity")
	if err != nil {
		t.Fatal(err)
	}
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(401)
		io.WriteString(w, `{"error":"invalid_client","error_description":"private-upstream-content"}`)
	}))
	defer upstream.Close()
	cfg.TokenURL = upstream.URL
	for _, override := range []string{"", "random-test-override"} {
		t.Setenv("ANTIGRAVITY_OAUTH_CLIENT_SECRET", override)
		_, err = Refresh(context.Background(), upstream.Client(), cfg, Token{RefreshToken: "private-test-refresh"}, ClientSecret(cfg))
		var status *UpstreamHTTPError
		if err == nil || !errors.As(err, &status) || status.Code != "invalid_client" {
			t.Fatal("upstream error identity lost")
		}
		if strings.Contains(err.Error(), "random-test-override") || strings.Contains(err.Error(), "private-upstream-content") || strings.Contains(err.Error(), "private-test-refresh") {
			t.Fatal("diagnostic leaked credential or upstream response")
		}
		if strings.Contains(err.Error(), "清空该变量") != (override != "") {
			t.Fatal("override diagnostic did not match effective config")
		}
	}
	t.Setenv("ANTIGRAVITY_OAUTH_CLIENT_SECRET", "")
	if ClientSecret(cfg) != antigravityDesktopClientSecret {
		t.Fatal("empty override did not restore paired builtin")
	}
}
