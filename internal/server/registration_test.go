package server

import (
	"context"
	"encoding/json"
	"errors"
	"fiftyapi/internal/store"
	"fmt"
	"golang.org/x/crypto/bcrypt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRegistrationLifecycle(t *testing.T) {
	ctx := context.Background()
	s, err := store.Open(ctx, filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	a, err := New(s, Config{})
	if err != nil {
		t.Fatal(err)
	}
	h := a.Handler()
	n := 0
	request := func(path, body, origin string, cookie *http.Cookie) *httptest.ResponseRecorder {
		n++
		r := httptest.NewRequest("POST", "http://example.test"+path, strings.NewReader(body))
		r.RemoteAddr = fmt.Sprintf("192.0.2.%d:1234", n)
		r.Header.Set("Content-Type", "application/json")
		if origin != "" {
			r.Header.Set("Origin", origin)
		}
		if cookie != nil {
			r.AddCookie(cookie)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	valid := `{"email":" New.User@Example.com ","real_name":" 张三 ","password":"registration-password"}`
	if w := request("/api/auth/register", valid, "", nil); w.Code != 403 {
		t.Fatalf("registered before setup: %d", w.Code)
	}
	if _, err = s.CreateAdmin(ctx, "admin", "admin-hash"); err != nil {
		t.Fatal(err)
	}
	if w := request("/api/auth/register", valid, "https://evil.test", nil); w.Code != 403 {
		t.Fatal("cross-origin registration accepted")
	}
	for _, body := range []string{
		`{}`, `{"email":"bad","real_name":"张三","password":"registration-password"}`,
		`{"email":"a@example.com","real_name":" ","password":"registration-password"}`,
		`{"email":"a@example.com","real_name":"张\n三","password":"registration-password"}`,
		`{"email":"a@example.com","real_name":"张三","password":"short"}`,
		`{"email":"a@example.com","real_name":"张三","password":"registration-password","role":"admin"}`,
		`{"email":"a@example.com","real_name":"张三","password":"registration-password","limit_5h_microusd":1000000}`,
	} {
		if w := request("/api/auth/register", body, "", nil); w.Code != 400 {
			t.Fatalf("invalid registration: %d %s", w.Code, w.Body.String())
		}
	}
	w := request("/api/auth/register", valid, "http://example.test", nil)
	if w.Code != 201 || len(w.Result().Cookies()) != 0 {
		t.Fatalf("registration: %d %s", w.Code, w.Body.String())
	}
	if strings.Contains(w.Body.String(), "password") || strings.Contains(w.Body.String(), "$2") {
		t.Fatal("registration exposed password")
	}
	u, err := s.UserByName(ctx, "new.user@example.com")
	if err != nil {
		t.Fatal(err)
	}
	if u.Email == nil || *u.Email != "new.user@example.com" || u.DisplayName != "张三" || u.Role != "user" || !u.Enabled || !u.PasswordSet || u.Limit5h != 0 || u.Limit7d != 0 {
		t.Fatalf("incorrect registered user: %+v", u)
	}
	if bcrypt.CompareHashAndPassword([]byte(u.PasswordHash), []byte("registration-password")) != nil {
		t.Fatal("password not securely hashed")
	}
	if w := request("/api/auth/register", valid, "", nil); w.Code != 409 {
		t.Fatalf("duplicate registration: %d", w.Code)
	}
	users, _ := s.Users(ctx)
	if len(users) != 2 {
		t.Fatal("invalid or duplicate registration created users")
	}
	login := request("/api/auth/login", `{"username":" NEW.USER@EXAMPLE.COM ","password":"registration-password"}`, "", nil)
	if login.Code != 200 {
		t.Fatalf("registered email login failed: %d %s", login.Code, login.Body.String())
	}
	var session struct {
		User store.User `json:"user"`
	}
	_ = json.Unmarshal(login.Body.Bytes(), &session)
	if session.User.ID != u.ID || len(login.Result().Cookies()) != 1 {
		t.Fatal("wrong login identity")
	}
	r := httptest.NewRequest("GET", "http://example.test/api/admin/users", nil)
	r.AddCookie(login.Result().Cookies()[0])
	out := httptest.NewRecorder()
	h.ServeHTTP(out, r)
	if out.Code != 403 {
		t.Fatal("registered member received admin access")
	}
	quota, err := s.Quota(ctx, u.ID)
	if err != nil || len(quota) != 2 {
		t.Fatalf("quota: %v", err)
	}
	for _, window := range quota {
		if window.Limit != 0 || window.Used != 0 || window.Reserved != 0 {
			t.Fatalf("nonzero initial quota: %+v", window)
		}
	}
	key, _, err := s.CreateAPIKey(ctx, u.ID, "test")
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Reserve(ctx, "registration-zero-quota", u.ID, key.ID, "model", store.Price{Input: 1, Output: 1}, 1, time.Now()); !errors.Is(err, store.ErrQuota) {
		t.Fatalf("zero-quota member could reserve usage: %v", err)
	}
	for i := 0; i < 6; i++ {
		r := httptest.NewRequest("POST", "http://example.test/api/auth/register", strings.NewReader(`{}`))
		r.RemoteAddr = "198.51.100.1:1234"
		r.Header.Set("Content-Type", "application/json")
		out := httptest.NewRecorder()
		h.ServeHTTP(out, r)
		if i == 5 && out.Code != 429 {
			t.Fatal("registration rate limit missing")
		}
	}
}
