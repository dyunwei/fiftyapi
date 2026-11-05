package server

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fiftyapi/internal/accounts"
	"fiftyapi/internal/store"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"
)

func TestSub2APIMigrationAdminPreviewImportAndLogin(t *testing.T) {
	ctx := context.Background()
	s, err := store.Open(ctx, filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	admin, _ := s.CreateAdminWithMigration(ctx, "admin", "hash", true)
	member, _ := s.CreateUser(ctx, admin, "existing", "hash", 0, 0)
	master := make([]byte, 32)
	api, err := New(s, Config{SettingsKey: master})
	if err != nil {
		t.Fatal(err)
	}
	svc, _ := accounts.New(s, master)
	api.AttachAccounts(svc)
	for token, user := range map[string]int64{strings.Repeat("a", 43): admin, strings.Repeat("m", 43): member} {
		hash := sha256.Sum256([]byte(token))
		s.SaveSession(ctx, hash[:], user, "csrf", time.Now().Add(time.Hour))
	}
	request := func(path, body, token, csrf string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", "http://example.test"+path, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("X-CSRF-Token", csrf)
		if token != "" {
			r.AddCookie(&http.Cookie{Name: cookieName, Value: token})
		}
		w := httptest.NewRecorder()
		api.Handler().ServeHTTP(w, r)
		return w
	}
	path := "/api/admin/migrations/sub2api"
	for _, tc := range []struct {
		token, csrf string
		code        int
	}{{"", "", 401}, {strings.Repeat("m", 43), "csrf", 403}, {strings.Repeat("a", 43), "", 403}} {
		if w := request(path, `{}`, tc.token, tc.csrf); w.Code != tc.code {
			t.Fatalf("boundary %d", w.Code)
		}
	}
	password, _ := bcrypt.GenerateFromPassword([]byte("old-password"), bcrypt.MinCost)
	b := sub2apiBundle{Type: "fiftyapi-sub2api-migration", Version: 1, Source: "old-site", Users: []store.MigrationUser{{ID: 1, Email: "old@example.com", PasswordHash: string(password), Status: "active"}}, Keys: []store.MigrationKey{{ID: 1, UserID: 1, Name: "old key", Key: "original-sub2api-key-fixture", Status: "active"}}, Accounts: []accounts.Sub2APIAccount{{ID: 1, Name: "old oauth", Platform: "openai", Type: "oauth", Status: "disabled", Credentials: json.RawMessage(`{"refresh_token":"upstream-refresh-fixture","chatgpt_account_id":"workspace-old"}`)}}}
	run := func(preview bool) *httptest.ResponseRecorder {
		raw, _ := json.Marshal(map[string]any{"data": b, "preview": preview})
		w := request(path, string(raw), strings.Repeat("a", 43), "csrf")
		if w.Code != 200 {
			t.Fatalf("migration failed %d: %s", w.Code, w.Body.String())
		}
		if strings.Contains(w.Body.String(), b.Keys[0].Key) || strings.Contains(w.Body.String(), "upstream-refresh-fixture") || strings.Contains(w.Body.String(), string(password)) {
			t.Fatal("secrets leaked")
		}
		return w
	}
	run(true)
	if _, err = s.UserByName(ctx, "old@example.com"); err == nil {
		t.Fatal("preview saved user")
	}
	run(false)
	if _, _, err = s.AuthenticateAPIKey(ctx, b.Keys[0].Key); err != nil {
		t.Fatal("original API key failed", err)
	}
	w := request("/api/auth/login", `{"username":"old@example.com","password":"old-password"}`, "", "")
	if w.Code != 200 {
		t.Fatal("original password failed", w.Code)
	}
	var login struct {
		User store.User `json:"user"`
	}
	json.Unmarshal(w.Body.Bytes(), &login)
	if login.User.Role != "user" || login.User.Limit5h != store.DefaultMemberBudget || login.User.Limit7d != store.DefaultMemberBudget {
		t.Fatal("wrong migrated privileges or budgets")
	}
	run(false)
	users, _ := s.Users(ctx)
	upstreams, _ := s.Accounts(ctx)
	if len(users) != 3 || len(upstreams) != 1 {
		t.Fatal("retry created duplicates")
	}
	if w := request(path, `{"data":{"type":"sub2api-data","version":1},"preview":true}`, strings.Repeat("a", 43), "csrf"); w.Code != 400 {
		t.Fatal("native account-only export treated as full migration")
	}
	if w := request("/api/admin/migrations/sub2api/complete", `{}`, strings.Repeat("a", 43), "csrf"); w.Code != 200 {
		t.Fatal("onboarding completion failed")
	}
	if w := request(path, `{}`, strings.Repeat("a", 43), "csrf"); w.Code != 403 {
		t.Fatal("migration allowed after onboarding completion")
	}

}

func TestInvitationDefaultsAndExplicitZeroBudgets(t *testing.T) {
	ctx := context.Background()
	s, err := store.Open(ctx, filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	admin, _ := s.CreateAdmin(ctx, "admin", "hash")
	api, err := New(s, Config{Mailer: &captureMailer{}, PublicURL: "https://gateway.example"})
	if err != nil {
		t.Fatal(err)
	}
	token := strings.Repeat("a", 43)
	hash := sha256.Sum256([]byte(token))
	s.SaveSession(ctx, hash[:], admin, "csrf", time.Now().Add(time.Hour))
	for _, tc := range []struct {
		body, email string
		budget      int64
	}{
		{`{"email":"default@example.com","display_name":"Default"}`, "default@example.com", store.DefaultMemberBudget},
		{`{"email":"zero@example.com","display_name":"Zero","limit_5h_microusd":0,"limit_7d_microusd":0}`, "zero@example.com", 0},
	} {
		r := httptest.NewRequest("POST", "http://example.test/api/admin/invitations", strings.NewReader(tc.body))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("X-CSRF-Token", "csrf")
		r.AddCookie(&http.Cookie{Name: cookieName, Value: token})
		w := httptest.NewRecorder()
		api.Handler().ServeHTTP(w, r)
		if w.Code != 201 {
			t.Fatalf("invitation failed %d", w.Code)
		}
		u, err := s.UserByName(ctx, tc.email)
		if err != nil || u.Limit5h != tc.budget || u.Limit7d != tc.budget {
			t.Fatal("incorrect invitation default", err)
		}
	}
}

func TestMigrationOnboardingSetupPersistsAndRequiresAdmin(t *testing.T) {
	ctx := context.Background()
	s, err := store.Open(ctx, filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	api, err := New(s, Config{SetupToken: strings.Repeat("s", 32)})
	if err != nil {
		t.Fatal(err)
	}
	var cookie *http.Cookie
	csrf := ""
	call := func(method, path, body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, "http://example.test"+path, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("X-CSRF-Token", csrf)
		if cookie != nil {
			r.AddCookie(cookie)
		}
		w := httptest.NewRecorder()
		api.Handler().ServeHTTP(w, r)
		return w
	}
	status := "/api/admin/migrations/sub2api/onboarding"
	if w := call("GET", status, ""); w.Code != 401 {
		t.Fatal("public onboarding state exposed")
	}
	if w := call("POST", "/api/setup", `{"username":"admin","password":"setup-password","setup_token":"`+strings.Repeat("s", 32)+`","migration":true}`); w.Code != 201 {
		t.Fatal("migration setup failed", w.Code)
	}
	value, _, err := s.Setting(ctx, "sub2api_onboarding_pending")
	if err != nil || value != "1" {
		t.Fatal("onboarding intent missing")
	}
	login := call("POST", "/api/auth/login", `{"username":"admin","password":"setup-password"}`)
	if login.Code != 200 {
		t.Fatal("login failed")
	}
	cookie = login.Result().Cookies()[0]
	var session struct {
		CSRF string `json:"csrf_token"`
	}
	json.Unmarshal(login.Body.Bytes(), &session)
	if w := call("GET", status, ""); w.Code != 200 || !strings.Contains(w.Body.String(), `"pending":true`) {
		t.Fatal("resume unavailable")
	}
	complete := "/api/admin/migrations/sub2api/complete"
	if w := call("POST", complete, `{}`); w.Code != 403 {
		t.Fatal("completion allowed without CSRF")
	}
	csrf = session.CSRF
	if w := call("POST", complete, `{}`); w.Code != 200 {
		t.Fatal("completion failed")
	}
	if w := call("GET", status, ""); !strings.Contains(w.Body.String(), `"pending":false`) {
		t.Fatal("completed onboarding resumed")
	}
}
