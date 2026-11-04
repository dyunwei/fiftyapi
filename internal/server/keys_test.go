package server

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fiftyapi/internal/store"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRevealKeyRequiresOwnerSessionAndCSRF(t *testing.T) {
	ctx := context.Background()
	s, err := store.Open(ctx, filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	owner, err := s.CreateAdmin(ctx, "owner", "hash")
	if err != nil {
		t.Fatal(err)
	}
	other, err := s.CreateUser(ctx, owner, "other", "hash", 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	master := bytes.Repeat([]byte{8}, 32)
	a, err := New(s, Config{SettingsKey: master})
	if err != nil {
		t.Fatal(err)
	}
	for token, user := range map[string]int64{strings.Repeat("o", 43): owner, strings.Repeat("x", 43): other} {
		hash := sha256.Sum256([]byte(token))
		if err = s.SaveSession(ctx, hash[:], user, "csrf", time.Now().Add(time.Hour)); err != nil {
			t.Fatal(err)
		}
	}
	handler := a.Handler()
	request := func(method, path, session, csrf, body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, "http://example.test"+path, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		if session != "" {
			r.AddCookie(&http.Cookie{Name: cookieName, Value: session})
		}
		if csrf != "" {
			r.Header.Set("X-CSRF-Token", csrf)
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	created := request("POST", "/api/keys", strings.Repeat("o", 43), "csrf", `{"name":"copyable"}`)
	if created.Code != 201 {
		t.Fatal(created.Code, created.Body.String())
	}
	var result struct {
		Key   store.APIKey `json:"key"`
		Token string       `json:"token"`
	}
	if err = json.Unmarshal(created.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if !result.Key.Copyable || !strings.HasPrefix(result.Token, "sk-") {
		t.Fatal("new key is not copyable")
	}
	path := fmt.Sprintf("/api/keys/%d/reveal", result.Key.ID)
	for _, tc := range []struct {
		session, csrf string
		status        int
	}{{"", "", 401}, {strings.Repeat("o", 43), "", 403}, {strings.Repeat("x", 43), "csrf", 404}, {strings.Repeat("o", 43), "csrf", 200}} {
		w := request("POST", path, tc.session, tc.csrf, "{}")
		if w.Code != tc.status {
			t.Fatalf("got %d want %d: %s", w.Code, tc.status, w.Body.String())
		}
		if w.Code == 200 && (w.Header().Get("Cache-Control") != "no-store" || !strings.Contains(w.Body.String(), result.Token)) {
			t.Fatal("copy response missing token or no-store")
		}
		if w.Code != 200 && strings.Contains(w.Body.String(), result.Token) {
			t.Fatal("unauthorized secret response")
		}
	}
	list := request("GET", "/api/keys", strings.Repeat("o", 43), "", "")
	if list.Code != 200 || strings.Contains(list.Body.String(), result.Token) || strings.Contains(list.Body.String(), "token_encrypted") {
		t.Fatal("list leaked key")
	}
	if err = s.RevokeAPIKey(ctx, owner, result.Key.ID); err != nil {
		t.Fatal(err)
	}
	if w := request("POST", path, strings.Repeat("o", 43), "csrf", "{}"); w.Code != 404 {
		t.Fatal("revoked key returned", w.Code)
	}
}
