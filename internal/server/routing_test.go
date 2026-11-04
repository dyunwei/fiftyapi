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
)

func TestRoutingManagementAuthorizationAndImport(t *testing.T) {
	ctx := context.Background()
	s, e := store.Open(ctx, filepath.Join(t.TempDir(), "db"))
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	admin, _ := s.CreateAdmin(ctx, "admin", "hash")
	member, _ := s.CreateUser(ctx, admin, "member", "hash", 100, 100)
	service, e := accounts.New(s, make([]byte, 32))
	if e != nil {
		t.Fatal(e)
	}
	api, e := New(s, Config{})
	if e != nil {
		t.Fatal(e)
	}
	api.AttachAccounts(service)
	account, e := s.CreateAccount(ctx, admin, "fixture", "openai", func(int64) ([]byte, error) { return []byte("private-encrypted-placeholder"), nil })
	if e != nil {
		t.Fatal(e)
	}
	for i, id := range []int64{admin, member} {
		token := strings.Repeat(string(rune('a'+i)), 43)
		digest := sha256.Sum256([]byte(token))
		if e = s.SaveSession(ctx, digest[:], id, "csrf", time.Now().Add(time.Hour)); e != nil {
			t.Fatal(e)
		}
	}
	request := func(method, path, token, csrf, body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("X-CSRF-Token", csrf)
		if token != "" {
			r.AddCookie(&http.Cookie{Name: cookieName, Value: token})
		}
		w := httptest.NewRecorder()
		api.Handler().ServeHTTP(w, r)
		return w
	}
	adminToken, memberToken := strings.Repeat("a", 43), strings.Repeat("b", 43)
	for _, tc := range []struct {
		method, path, token, csrf, body string
		status                          int
	}{
		{"GET", "/api/admin/accounts/1/routing", "", "", "", 401},
		{"GET", "/api/admin/accounts/1/routing", memberToken, "", "", 403},
		{"GET", "/api/admin/accounts/1/routing", adminToken, "", "", 200},
		{"PUT", "/api/admin/accounts/1/routing", adminToken, "", `{}`, 403},
		{"PUT", "/api/admin/accounts/1/routing", adminToken, "csrf", `{"priority":10,"weight":4,"concurrency":3,"max_queue":8,"wait_ms":100}`, 200},
		{"POST", "/api/admin/accounts/1/routing/reset", memberToken, "csrf", `{}`, 403},
		{"POST", "/api/admin/accounts/1/routing/reset", adminToken, "csrf", `{}`, 200},
		{"POST", "/api/admin/accounts/import", memberToken, "csrf", `{}`, 403},
		{"POST", "/api/admin/accounts/import", adminToken, "", `{}`, 403},
	} {
		w := request(tc.method, tc.path, tc.token, tc.csrf, tc.body)
		if w.Code != tc.status {
			t.Errorf("%s %s: got %d, want %d", tc.method, tc.path, w.Code, tc.status)
		}
		if strings.Contains(w.Body.String(), "private-encrypted") {
			t.Fatal("credential leaked")
		}
	}
	raw := `{"provider":"openai","verify":false,"accounts":[{"name":"imported","auth":{"access_token":"fixture-import-access","refresh_token":"fixture-import-refresh","expires_in":3600,"config":{"models_url":"https://untrusted.example"}}}]}`
	w := request("POST", "/api/admin/accounts/import", adminToken, "csrf", raw)
	if w.Code != 200 {
		t.Fatalf("batch import: %d", w.Code)
	}
	if strings.Contains(w.Body.String(), "fixture-import-") {
		t.Fatal("batch response leaked tokens")
	}
	var result struct {
		Imported int `json:"imported"`
	}
	if json.Unmarshal(w.Body.Bytes(), &result) != nil || result.Imported != 1 {
		t.Fatal("batch result incorrect")
	}
	if w = request("DELETE", "/api/admin/accounts/1", adminToken, "csrf", ""); w.Code != 200 {
		t.Fatal("admin delete failed")
	}
	if _, e = s.Account(ctx, account); e == nil {
		t.Fatal("deleted account still accessible")
	}
	if w = request("GET", "/api/admin/accounts/1/routing", adminToken, "", ""); w.Code != 404 {
		t.Fatal("deleted routing still exposed")
	}
}
