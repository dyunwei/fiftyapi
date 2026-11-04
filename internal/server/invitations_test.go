package server

import (
	"context"
	"encoding/json"
	"errors"
	"fiftyapi/internal/mailer"
	"fiftyapi/internal/store"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
)

type captureMailer struct {
	messages []mailer.Message
	fail     bool
}

func (m *captureMailer) SendInvitation(_ context.Context, message mailer.Message) error {
	m.messages = append(m.messages, message)
	if m.fail {
		return errors.New("private SMTP error")
	}
	return nil
}
func TestEmailInvitationHTTPAndEightByteUserPassword(t *testing.T) {
	s, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	sender := &captureMailer{fail: true}
	api, err := New(s, Config{SetupToken: strings.Repeat("s", 32), Mailer: sender, PublicURL: "https://gateway.example"})
	if err != nil {
		t.Fatal(err)
	}
	var cookie *http.Cookie
	csrf := ""
	request := func(method, path string, body any, auth bool) *httptest.ResponseRecorder {
		raw, _ := json.Marshal(body)
		r := httptest.NewRequest(method, "http://example.test"+path, strings.NewReader(string(raw)))
		r.Header.Set("Content-Type", "application/json")
		if auth && cookie != nil {
			r.AddCookie(cookie)
			r.Header.Set("X-CSRF-Token", csrf)
		}
		w := httptest.NewRecorder()
		api.Handler().ServeHTTP(w, r)
		return w
	}
	w := request("POST", "/api/setup", map[string]string{"username": "admin", "password": "abcdefgh", "setup_token": strings.Repeat("s", 32)}, false)
	if w.Code != 201 {
		t.Fatal("setup failed")
	}
	w = request("POST", "/api/auth/login", map[string]string{"username": "admin", "password": "abcdefgh"}, false)
	if w.Code != 200 {
		t.Fatal("admin login failed")
	}
	cookie = w.Result().Cookies()[0]
	var login struct {
		CSRF string `json:"csrf_token"`
	}
	json.Unmarshal(w.Body.Bytes(), &login)
	csrf = login.CSRF
	email := strings.Repeat("a", 60) + "@example.test"
	in := map[string]any{"email": email, "display_name": "Alice", "limit_5h_microusd": 1000000, "limit_7d_microusd": 5000000}
	if w = request("POST", "/api/admin/invitations", in, false); w.Code != 401 {
		t.Fatal("anonymous invitation allowed")
	}
	w = request("POST", "/api/admin/invitations", in, true)
	if w.Code != 201 || !strings.Contains(w.Body.String(), "failed") || strings.Contains(w.Body.String(), "private SMTP error") {
		t.Fatal("failed delivery not safely exposed")
	}
	var created struct{ ID int64 }
	json.Unmarshal(w.Body.Bytes(), &created)
	extract := func(message mailer.Message) string {
		u, e := url.Parse(message.URL)
		if e != nil {
			t.Fatal(e)
		}
		return strings.TrimPrefix(u.Fragment, "invite=")
	}
	first := extract(sender.messages[0])
	if strings.Contains(w.Body.String(), first) {
		t.Fatal("token exposed in API response")
	}
	sender.fail = false
	w = request("POST", fmt.Sprintf("/api/admin/users/%d/invitation", created.ID), map[string]any{}, true)
	if w.Code != 200 {
		t.Fatal("failed-mail resend failed")
	}
	token := extract(sender.messages[1])
	if w = request("POST", "/api/invitations/check", map[string]string{"token": first}, false); w.Code != 400 {
		t.Fatal("old link usable")
	}
	if w = request("POST", "/api/invitations/check", map[string]string{"token": token}, false); w.Code != 200 {
		t.Fatal("new link unusable")
	}
	if w = request("POST", "/api/invitations/accept", map[string]string{"token": token, "password": "1234567"}, false); w.Code != 400 {
		t.Fatal("seven-byte user password accepted")
	}
	if w = request("POST", "/api/invitations/accept", map[string]string{"token": token, "password": "12345678"}, false); w.Code != 200 {
		t.Fatal("eight-byte invitation password rejected")
	}
	if w = request("POST", "/api/invitations/accept", map[string]string{"token": token, "password": "different"}, false); w.Code != 400 {
		t.Fatal("invitation reused")
	}
	w = request("POST", "/api/auth/login", map[string]string{"username": email, "password": "12345678"}, false)
	if w.Code != 200 {
		t.Fatal("long email/eight-byte password login failed")
	}
	cookie = w.Result().Cookies()[0]
	json.Unmarshal(w.Body.Bytes(), &login)
	csrf = login.CSRF
	if w = request("POST", "/api/admin/invitations", in, true); w.Code != 403 {
		t.Fatal("member could send invitation")
	}
	if w = request("PUT", "/api/me/password", map[string]string{"old_password": "12345678", "password": "abcdefgh"}, true); w.Code != 200 {
		t.Fatal("member could not change to eight-byte password")
	}
	rows, err := s.Users(context.Background())
	if err != nil || rows[1].MailState != "" || !rows[1].PasswordSet {
		t.Fatal("password change did not revoke old invitation")
	}
}

func TestInvitationAndLoginRateLimitsIndependent(t *testing.T) {
	api := &API{attempts: map[string]attempt{}}
	request := httptest.NewRequest("POST", "http://example.test", nil)
	for i := 0; i < 10; i++ {
		if !api.allowLogin(request) {
			t.Fatal("login rate limit too early")
		}
	}
	if api.allowLogin(request) {
		t.Fatal("login rate limit missing")
	}
	for i := 0; i < 30; i++ {
		if !api.allowAttempt(request, "invitation-check", 30) {
			t.Fatal("invitation check consumed login quota")
		}
	}
	if api.allowAttempt(request, "invitation-check", 30) {
		t.Fatal("invitation check rate limit missing")
	}
	if !api.allowAttempt(request, "invitation-accept", 10) {
		t.Fatal("invitation accept shares unrelated counter")
	}
}
