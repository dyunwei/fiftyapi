package server

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"fiftyapi/internal/accounts"
	"fiftyapi/internal/store"
)

func TestPublicContributionBoundary(t *testing.T) {
	ctx := context.Background()
	s, err := store.Open(ctx, filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	actor, _ := s.CreateAdmin(ctx, "admin", "hash")
	member, _ := s.CreateUser(ctx, actor, "member", "hash", 0, 0)
	a, err := New(s, Config{})
	if err != nil {
		t.Fatal(err)
	}
	service, err := accounts.New(s, make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	a.AttachAccounts(service)
	adminToken := strings.Repeat("a", 43)
	memberToken := strings.Repeat("b", 43)
	for token, user := range map[string]int64{adminToken: actor, memberToken: member} {
		hash := sha256.Sum256([]byte(token))
		if err = s.SaveSession(ctx, hash[:], user, "csrf", time.Now().Add(time.Hour)); err != nil {
			t.Fatal(err)
		}
	}
	handler := a.Handler()
	sequence := 0
	request := func(path, body, token, csrf string) *httptest.ResponseRecorder {
		sequence++
		r := httptest.NewRequest("POST", "http://example.test"+path, strings.NewReader(body))
		r.RemoteAddr = "test-" + strings.Repeat("x", sequence)
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("X-CSRF-Token", csrf)
		if token != "" {
			r.AddCookie(&http.Cookie{Name: cookieName, Value: token})
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	payload := `{"provider":"openai","note":"team"}`
	for _, tc := range []struct {
		token, csrf string
		status      int
	}{{"", "", 401}, {memberToken, "csrf", 403}, {adminToken, "", 403}} {
		if w := request("/api/admin/contribution-links", payload, tc.token, tc.csrf); w.Code != tc.status {
			t.Fatalf("link boundary: %d", w.Code)
		}
	}
	w := request("/api/admin/contribution-links", payload, adminToken, "csrf")
	if w.Code != 201 {
		t.Fatal(w.Body.String())
	}
	var created struct {
		ID        int64
		Fragment  string
		ExpiresAt int64 `json:"expires_at"`
	}
	if err = json.Unmarshal(w.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	if delta := created.ExpiresAt - time.Now().Unix(); delta < 86395 || delta > 86400 {
		t.Fatal("default expiry is not 24h")
	}
	token := strings.TrimPrefix(created.Fragment, "#contribute=")
	body := `{"token":"` + token + `"}`
	if w = request("/api/contributions/check", body, "", ""); w.Code != 200 || strings.Contains(w.Body.String(), "team") {
		t.Fatal("public check failed or exposed metadata")
	}
	// Public OAuth uses only the link's provider and cannot reauthorize existing accounts.
	oauthStart := `{"token":"` + token + `","name":"browser-owner"}`
	for _, extra := range []string{`,"provider":"claude"`, `,"account_id":1`, `,"enabled":true`} {
		if w = request("/api/contributions/oauth/start", strings.TrimSuffix(oauthStart, "}")+extra+"}", "", ""); w.Code != 400 {
			t.Fatal("public OAuth accepted advanced fields")
		}
	}
	w = request("/api/contributions/oauth/start", oauthStart, "", "")
	var started accounts.OAuthStart
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &started) != nil || !strings.Contains(started.AuthURL, "auth.openai.com") {
		t.Fatalf("public OAuth start failed: %s", w.Body.String())
	}
	oauthComplete := `{"token":"` + token + `","session_id":"` + started.SessionID + `","result":"wrong#state"}`
	if w = request("/api/contributions/oauth/complete", oauthComplete, "", ""); w.Code != 400 || strings.Contains(w.Body.String(), "wrong#state") {
		t.Fatal("invalid OAuth result accepted or exposed")
	}
	auth := `{"refresh_token":"test-contribution-refresh","proxy_url":"http://unsafe.test","max_concurrency":99,"enabled":true}`
	submit := `{"token":"` + token + `","accounts":[{"name":"owner-account","auth":` + auth + `}]}`
	if w = request("/api/contributions/import", strings.TrimSuffix(submit, "}")+`,"enabled":true}`, "", ""); w.Code != 400 {
		t.Fatal("public advanced field accepted")
	}
	w = request("/api/contributions/import", submit, "", "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"imported":1`) || strings.Contains(w.Body.String(), "test-contribution-refresh") || strings.Contains(w.Body.String(), `"id"`) {
		t.Fatalf("import failed or leaked credentials: %s", w.Body.String())
	}
	records, err := s.Accounts(ctx)
	if err != nil || len(records) != 1 || records[0].Enabled || records[0].ContributionLinkID == nil {
		t.Fatalf("contribution not disabled or provenance lost: %v %v", records, err)
	}
	cfg, err := service.RuntimeConfig(ctx, records[0].ID)
	if err != nil || cfg.ProxyURL != "" || cfg.MaxConcurrency == 99 {
		t.Fatal("untrusted config imported")
	}
	if _, err = service.Verify(ctx, records[0].ID); err == nil {
		t.Fatal("disabled account entered verification")
	}
	duplicate := strings.Replace(submit, "owner-account", "other-name", 1)
	if w = request("/api/contributions/import", duplicate, "", ""); !strings.Contains(w.Body.String(), `"imported":0`) {
		t.Fatal("duplicate authorized file accepted")
	}
	if _, err = s.DB.Exec("UPDATE account_contribution_links SET revoked_at=? WHERE id=?", time.Now().Unix(), created.ID); err != nil {
		t.Fatal(err)
	}
	if w = request("/api/contributions/import", submit, "", ""); w.Code != 410 {
		t.Fatal("revoked link accepted")
	}
	for _, path := range []string{"/api/contributions/oauth/start", "/api/contributions/oauth/complete"} {
		payload := oauthStart
		if strings.HasSuffix(path, "complete") {
			payload = oauthComplete
		}
		if w = request(path, payload, "", ""); w.Code != 410 {
			t.Fatal("revoked link accepted for OAuth")
		}
	}
	if w = request("/api/contributions/check", `{"token":"`+strings.Repeat("z", 43)+`"}`, "", ""); w.Code != 410 {
		t.Fatal("unknown link accepted")
	}
}
