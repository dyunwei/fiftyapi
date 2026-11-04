package server

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"fiftyapi/internal/credentials"
	"fiftyapi/internal/mailer"
	"fiftyapi/internal/store"
)

func TestEmailSettingsEncryptionPermissionsAndImmediateOverride(t *testing.T) {
	ctx := context.Background()
	s, err := store.Open(ctx, filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	admin, err := s.CreateAdmin(ctx, "admin", "hash")
	if err != nil {
		t.Fatal(err)
	}
	member, err := s.CreateUser(ctx, admin, "member", "hash", 100, 1000)
	if err != nil {
		t.Fatal(err)
	}
	key := make([]byte, 32)
	cfg := Config{SettingsKey: key, PublicURL: "https://env.example", Mailer: &mailer.SMTP{Host: "127.0.0.1", Port: "2525", From: "env@example.test", Username: "env-user", Password: "env-private-fixture", TLSMode: "none"}}
	a, err := New(s, cfg)
	if err != nil {
		t.Fatal(err)
	}
	for i, u := range []int64{admin, member} {
		hash := sha256.Sum256([]byte(strings.Repeat(string(rune('a'+i)), 43)))
		if err = s.SaveSession(ctx, hash[:], u, "csrf", time.Now().Add(time.Hour)); err != nil {
			t.Fatal(err)
		}
	}
	request := func(method, path string, body any, token, csrf string) *httptest.ResponseRecorder {
		raw, _ := json.Marshal(body)
		r := httptest.NewRequest(method, path, strings.NewReader(string(raw)))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("X-CSRF-Token", csrf)
		if token != "" {
			r.AddCookie(&http.Cookie{Name: cookieName, Value: token})
		}
		w := httptest.NewRecorder()
		a.Handler().ServeHTTP(w, r)
		return w
	}
	adminToken, memberToken := strings.Repeat("a", 43), strings.Repeat("b", 43)
	for _, method := range []string{"GET", "PUT", "DELETE", "POST"} {
		path := "/api/admin/settings/email"
		if method == "POST" {
			path += "/test"
		}
		if w := request(method, path, nil, memberToken, "csrf"); w.Code != 403 {
			t.Fatal("member email settings access", method, w.Code)
		}
	}
	if w := request("GET", "/api/admin/settings/email", nil, "", ""); w.Code != 401 {
		t.Fatal("anonymous email settings access")
	}
	w := request("GET", "/api/admin/settings/email", nil, adminToken, "")
	if w.Code != 200 || strings.Contains(w.Body.String(), "env-private-fixture") {
		t.Fatal("environment password leaked")
	}
	in := map[string]any{"enabled": true, "public_url": "https://saved.example", "host": "127.0.0.1", "port": "2525", "from": "saved@example.test", "username": "saved-user", "password": "stored-private-fixture", "tls_mode": "none"}
	if w = request("PUT", "/api/admin/settings/email", in, adminToken, ""); w.Code != 403 {
		t.Fatal("missing CSRF accepted")
	}
	if w = request("PUT", "/api/admin/settings/email", in, adminToken, "csrf"); w.Code != 200 {
		t.Fatalf("save config: %d %s", w.Code, w.Body.String())
	}
	encoded, exists, err := s.Setting(ctx, emailSettingName)
	if err != nil || !exists || strings.Contains(encoded, "stored-private-fixture") {
		t.Fatal("unencrypted settings")
	}
	sealed, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		t.Fatal(err)
	}
	plain, err := credentials.Decrypt(key, emailSettingBinding, sealed)
	if err != nil || !strings.Contains(string(plain), "stored-private-fixture") {
		t.Fatal("settings not protected by original master key", err)
	}
	for i := 0; i < 2; i++ {
		w = request("GET", "/api/admin/settings/email", nil, adminToken, "")
		if w.Code != 200 || strings.Contains(w.Body.String(), "stored-private-fixture") || !strings.Contains(w.Body.String(), `"password_set":true`) {
			t.Fatal("secret readback or missing password marker")
		}
		if i == 0 {
			a, err = New(s, cfg)
			if err != nil {
				t.Fatal(err)
			}
		}
	}
	in["password"] = ""
	if w = request("PUT", "/api/admin/settings/email", in, adminToken, "csrf"); w.Code != 200 {
		t.Fatal("blank password save failed")
	}
	saved, _, err := a.readEmailSettings(ctx)
	if err != nil || saved.Password != "stored-private-fixture" {
		t.Fatal("blank password did not preserve stored value", err)
	}
	sender, url, err := a.invitationSender(ctx)
	if err != nil || sender == nil || url != "https://saved.example" {
		t.Fatal("live config not used for invitations", err)
	}
	in["clear_password"] = true
	if w = request("PUT", "/api/admin/settings/email", in, adminToken, "csrf"); w.Code != 200 {
		t.Fatal("clear password failed")
	}
	saved, _, _ = a.readEmailSettings(ctx)
	if saved.Password != "" {
		t.Fatal("explicit password clear ignored")
	}
	in["host"] = "remote.example"
	if w = request("PUT", "/api/admin/settings/email", in, adminToken, "csrf"); w.Code != 400 {
		t.Fatal("remote plaintext SMTP accepted")
	}
	in["host"] = "127.0.0.1"
	in["enabled"] = false
	if w = request("PUT", "/api/admin/settings/email", in, adminToken, "csrf"); w.Code != 200 {
		t.Fatal("disable failed")
	}
	w = request("GET", "/api/admin/invitations/config", nil, adminToken, "")
	if !strings.Contains(w.Body.String(), `"configured":false`) {
		t.Fatal("disabled settings fell back to environment")
	}
	audit, err := s.AuditLogs(ctx)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(audit)
	if strings.Contains(string(raw), "private-fixture") || strings.Contains(string(raw), "saved-user") {
		t.Fatal("SMTP credentials leaked in audit")
	}
	if w = request("DELETE", "/api/admin/settings/email", nil, adminToken, "csrf"); w.Code != 200 {
		t.Fatal("restore environment failed")
	}
	sender, url, err = a.invitationSender(ctx)
	if err != nil || sender == nil || url != "https://env.example" {
		t.Fatal("environment fallback not restored", err)
	}
	if _, err = s.DB.Exec("INSERT INTO settings(key,value) VALUES(?,'corrupted')", emailSettingName); err != nil {
		t.Fatal(err)
	}
	if w = request("GET", "/api/admin/invitations/config", nil, adminToken, ""); w.Code != 500 {
		t.Fatal("corrupt config silently fell back to environment")
	}
}
