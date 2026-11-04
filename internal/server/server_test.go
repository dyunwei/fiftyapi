package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"fiftyapi/internal/store"
)

func TestAuthenticationAndAdminBoundary(t *testing.T) {
	s, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	api, err := New(s, Config{SetupToken: strings.Repeat("s", 8)})
	if err != nil {
		t.Fatal(err)
	}
	handler := api.Handler()
	request := func(method, path, body string, cookie *http.Cookie, csrf, origin string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, "http://example.test"+path, bytes.NewBufferString(body))
		r.Header.Set("Content-Type", "application/json")
		if cookie != nil {
			r.AddCookie(cookie)
		}
		if csrf != "" {
			r.Header.Set("X-CSRF-Token", csrf)
		}
		if origin != "" {
			r.Header.Set("Origin", origin)
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	setup := fmt.Sprintf(`{"username":"admin","password":"long-admin-password","setup_token":%q}`, strings.Repeat("s", 8))
	if w := request("POST", "/api/setup", setup, nil, "", "https://evil.test"); w.Code != 403 {
		t.Fatal("accepted cross-origin setup")
	}
	if w := request("POST", "/api/setup", strings.Replace(setup, strings.Repeat("s", 8), "wrong", 1), nil, "", ""); w.Code != 403 {
		t.Fatal("accepted invalid setup token")
	}
	if w := request("POST", "/api/setup", setup, nil, "", ""); w.Code != 201 {
		t.Fatalf("setup: %d %s", w.Code, w.Body.String())
	}
	if w := request("POST", "/api/setup", setup, nil, "", ""); w.Code != 409 {
		t.Fatal("setup repeated")
	}
	if w := request("POST", "/api/auth/register", `{}`, nil, "", ""); w.Code != 400 {
		t.Fatal("registration accepted missing required fields")
	}
	if w := request("GET", "/api/admin/users", "", nil, "", ""); w.Code != 401 {
		t.Fatal("unauthenticated access")
	}
	login := func(name, password string) (*http.Cookie, string) {
		body, _ := json.Marshal(map[string]string{"username": name, "password": password})
		w := request("POST", "/api/auth/login", string(body), nil, "", "")
		if w.Code != 200 {
			t.Fatalf("login: %d %s", w.Code, w.Body.String())
		}
		if strings.Contains(w.Body.String(), "password_hash") || strings.Contains(w.Body.String(), "$2a$") {
			t.Fatal("password hash leaked")
		}
		var result struct {
			CSRF string `json:"csrf_token"`
		}
		json.Unmarshal(w.Body.Bytes(), &result)
		cookies := w.Result().Cookies()
		if len(cookies) != 1 || !cookies[0].HttpOnly || cookies[0].SameSite != http.SameSiteStrictMode {
			t.Fatal("unsafe cookie")
		}
		return cookies[0], result.CSRF
	}
	admin, csrf := login("admin", "long-admin-password")
	create := `{"username":"alice","password":"long-alice-password","limit_5h_microusd":1000000,"limit_7d_microusd":5000000}`
	if w := request("POST", "/api/admin/users", create, admin, "", ""); w.Code != 403 {
		t.Fatal("accepted missing CSRF")
	}
	w := request("POST", "/api/admin/users", create, admin, csrf, "")
	if w.Code != 201 {
		t.Fatalf("create user: %d %s", w.Code, w.Body.String())
	}
	var created struct {
		ID int64 `json:"id"`
	}
	json.Unmarshal(w.Body.Bytes(), &created)
	alice, aliceCSRF := login("alice", "long-alice-password")
	if w := request("GET", "/api/admin/users", "", alice, "", ""); w.Code != 403 {
		t.Fatal("user accessed admin users")
	}
	if w := request("GET", "/api/admin/accounts", "", alice, "", ""); w.Code != 403 {
		t.Fatal("user accessed upstream accounts")
	}
	if w := request("POST", "/api/auth/logout", `{}`, alice, aliceCSRF, ""); w.Code != 200 {
		t.Fatal("logout failed")
	}
	if w := request("GET", "/api/me", "", alice, "", ""); w.Code != 401 {
		t.Fatal("logout did not revoke session")
	}
	alice, _ = login("alice", "long-alice-password")
	if w := request("PUT", fmt.Sprintf("/api/admin/users/%d/enabled", created.ID), `{"enabled":false}`, admin, csrf, ""); w.Code != 200 {
		t.Fatal("disable failed")
	}
	if w := request("GET", "/api/me", "", alice, "", ""); w.Code != 401 {
		t.Fatal("disabled user retained session")
	}
	if w := request("POST", "/api/auth/login", `{"username":"alice","password":"long-alice-password"}`, nil, "", ""); w.Code != 401 {
		t.Fatal("disabled user logged in")
	}
}

func TestAdminPasswordEightByteMinimum(t *testing.T) {
	for _, item := range []struct {
		password, role string
		valid          bool
	}{{"1234567", "admin", false}, {"12345678", "admin", true}, {strings.Repeat("a", 72), "admin", true}, {strings.Repeat("a", 73), "admin", false}, {"12345678", "user", true}, {"123456789012", "user", true}} {
		if validPassword(item.password, item.role) != item.valid {
			t.Errorf("password length %d role %s validation mismatch", len(item.password), item.role)
		}
	}
	s, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	api, err := New(s, Config{SetupToken: strings.Repeat("s", 8)})
	if err != nil {
		t.Fatal(err)
	}
	request := func(method, path, body string, cookie *http.Cookie, csrf string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, "http://example.test"+path, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		if cookie != nil {
			r.AddCookie(cookie)
		}
		r.Header.Set("X-CSRF-Token", csrf)
		w := httptest.NewRecorder()
		api.Handler().ServeHTTP(w, r)
		return w
	}
	for _, password := range []string{"1234567", "12345678"} {
		body, _ := json.Marshal(map[string]string{"username": "admin", "password": password, "setup_token": strings.Repeat("s", 8)})
		w := request("POST", "/api/setup", string(body), nil, "")
		want := 400
		if len(password) == 8 {
			want = 201
		}
		if w.Code != want {
			t.Fatalf("setup with length %d: status %d", len(password), w.Code)
		}
	}
	login := request("POST", "/api/auth/login", `{"username":"admin","password":"12345678"}`, nil, "")
	if login.Code != 200 {
		t.Fatal("eight-byte admin password could not log in")
	}
	var result struct {
		CSRF string `json:"csrf_token"`
	}
	json.Unmarshal(login.Body.Bytes(), &result)
	cookie := login.Result().Cookies()[0]
	for _, password := range []string{"1234567", "abcdefgh"} {
		body, _ := json.Marshal(map[string]string{"old_password": "12345678", "password": password})
		w := request("PUT", "/api/me/password", string(body), cookie, result.CSRF)
		want := 400
		if len(password) == 8 {
			want = 200
		}
		if w.Code != want {
			t.Fatalf("admin password change with length %d: status %d", len(password), w.Code)
		}
	}
	if w := request("POST", "/api/auth/login", `{"username":"admin","password":"abcdefgh"}`, nil, ""); w.Code != 200 {
		t.Fatal("changed eight-byte admin password could not log in")
	}
}
