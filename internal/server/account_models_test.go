package server

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fiftyapi/internal/store"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestAccountModelsAdminBoundary(t *testing.T) {
	ctx := context.Background()
	s, e := store.Open(ctx, filepath.Join(t.TempDir(), "db"))
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	admin, e := s.CreateAdmin(ctx, "admin", "hash")
	if e != nil {
		t.Fatal(e)
	}
	member, e := s.CreateUser(ctx, admin, "member", "hash", 100, 100)
	if e != nil {
		t.Fatal(e)
	}
	account, e := s.CreateAccount(ctx, admin, "account", "openai", func(int64) ([]byte, error) { return []byte("private-ciphertext-placeholder"), nil })
	if e != nil {
		t.Fatal(e)
	}
	if e = s.SyncModels(ctx, account, "openai", []string{"unpriced-model"}); e != nil {
		t.Fatal(e)
	}
	for i, id := range []int64{admin, member} {
		token := strings.Repeat(string(rune('a'+i)), 43)
		hash := sha256.Sum256([]byte(token))
		if e = s.SaveSession(ctx, hash[:], id, "csrf", time.Now().Add(time.Hour)); e != nil {
			t.Fatal(e)
		}
	}
	api, e := New(s, Config{})
	if e != nil {
		t.Fatal(e)
	}
	for _, tc := range []struct {
		path, token string
		want        int
	}{
		{"/api/admin/accounts/1/models", "", 401},
		{"/api/admin/accounts/1/models", strings.Repeat("b", 43), 403},
		{"/api/admin/accounts/1/models", strings.Repeat("a", 43), 200},
		{"/api/admin/accounts/99/models", strings.Repeat("a", 43), 404},
		{"/api/admin/accounts/no/models", strings.Repeat("a", 43), 400},
		{"/api/admin/accounts/1/models?page=0", strings.Repeat("a", 43), 400},
		{"/api/admin/accounts/1/models?page=1&page=2", strings.Repeat("a", 43), 400},
		{"/api/admin/accounts/1/models?page_size=101", strings.Repeat("a", 43), 400},
		{"/api/admin/accounts/1/models?search=" + strings.Repeat("x", 257), strings.Repeat("a", 43), 400},
	} {
		req := httptest.NewRequest("GET", tc.path, nil)
		if tc.token != "" {
			req.AddCookie(&http.Cookie{Name: cookieName, Value: tc.token})
		}
		w := httptest.NewRecorder()
		api.Handler().ServeHTTP(w, req)
		if w.Code != tc.want {
			t.Errorf("%s: got %d, want %d", tc.path, w.Code, tc.want)
		}
		if strings.Contains(w.Body.String(), "private-ciphertext-placeholder") {
			t.Fatal("credential leaked")
		}
		if tc.want == 200 {
			var page store.AccountModelPage
			if json.Unmarshal(w.Body.Bytes(), &page) != nil || page.Total != 1 || page.Items[0].Priced || page.SyncedAt == nil {
				t.Fatal("unpriced model omitted or sync metadata missing")
			}
		}
	}
	for _, tc := range []struct {
		token, csrf string
		want        int
	}{
		{"", "", 401},
		{strings.Repeat("b", 43), "csrf", 403},
		{strings.Repeat("a", 43), "", 403},
		{strings.Repeat("a", 43), "csrf", 503},
	} {
		req := httptest.NewRequest("POST", "/api/admin/accounts/1/models/test", strings.NewReader(`{"model":"unpriced-model","protocol":"responses"}`))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-CSRF-Token", tc.csrf)
		if tc.token != "" {
			req.AddCookie(&http.Cookie{Name: cookieName, Value: tc.token})
		}
		w := httptest.NewRecorder()
		api.Handler().ServeHTTP(w, req)
		if w.Code != tc.want {
			t.Errorf("model test boundary: got %d want %d", w.Code, tc.want)
		}
	}

}
