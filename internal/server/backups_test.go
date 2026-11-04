package server

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fiftyapi/internal/backup"
	"fiftyapi/internal/store"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestBackupAdminBoundaryAndSettingsMasking(t *testing.T) {
	ctx := context.Background()
	db, err := store.Open(ctx, filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	admin, err := db.CreateAdmin(ctx, "admin", "hash")
	if err != nil {
		t.Fatal(err)
	}
	member, err := db.CreateUser(ctx, admin, "member", "hash", 100, 100)
	if err != nil {
		t.Fatal(err)
	}
	for i, id := range []int64{admin, member} {
		token := strings.Repeat(string(rune('a'+i)), 43)
		hash := sha256.Sum256([]byte(token))
		if err = db.SaveSession(ctx, hash[:], id, "csrf", time.Now().Add(time.Hour)); err != nil {
			t.Fatal(err)
		}
	}
	api, err := New(db, Config{SettingsKey: make([]byte, 32)})
	if err != nil {
		t.Fatal(err)
	}
	defer api.Shutdown(ctx)
	handler := api.Handler()
	for _, route := range []struct{ method, path string }{{"GET", "/api/admin/settings/backup"}, {"PUT", "/api/admin/settings/backup"}, {"POST", "/api/admin/settings/backup/test"}, {"GET", "/api/admin/backups"}, {"POST", "/api/admin/backups"}, {"GET", "/api/admin/backups/missing/download"}, {"DELETE", "/api/admin/backups/missing"}} {
		for _, tc := range []struct {
			token, csrf string
			want        int
		}{{"", "", 401}, {strings.Repeat("b", 43), "csrf", 403}} {
			req := httptest.NewRequest(route.method, route.path, strings.NewReader(`{}`))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("X-CSRF-Token", tc.csrf)
			if tc.token != "" {
				req.AddCookie(&http.Cookie{Name: cookieName, Value: tc.token})
			}
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, req)
			if w.Code != tc.want {
				t.Errorf("%s %s: got %d want %d", route.method, route.path, w.Code, tc.want)
			}
		}
		if route.method != "GET" {
			req := httptest.NewRequest(route.method, route.path, strings.NewReader(`{}`))
			req.AddCookie(&http.Cookie{Name: cookieName, Value: strings.Repeat("a", 43)})
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, req)
			if w.Code != 403 {
				t.Error("CSRF not enforced", route.path, w.Code)
			}
		}
	}
	request := func(method, path, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.AddCookie(&http.Cookie{Name: cookieName, Value: strings.Repeat("a", 43)})
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-CSRF-Token", "csrf")
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		return w
	}
	initial := request("GET", "/api/admin/settings/backup", "")
	if initial.Code != 200 || !strings.Contains(initial.Body.String(), `"retention_days":14`) {
		t.Fatal("missing default config", initial.Body.String())
	}
	// Without configured storage, a manual request must fail without launching a job.
	unconfigured := request("POST", "/api/admin/backups", `{}`)
	if unconfigured.Code != 400 {
		t.Fatal("unconfigured backup accepted", unconfigured.Code)
	}
	c := backup.DefaultConfig()
	c.Endpoint = "https://storage.example.com"
	c.Bucket = "backup-test"
	c.AccessKey = "private-storage-access"
	c.SecretKey = "private-storage-secret"
	raw, _ := json.Marshal(c)
	saved := request("PUT", "/api/admin/settings/backup", string(raw))
	if saved.Code != 200 {
		t.Fatal(saved.Body.String())
	}
	masked := request("GET", "/api/admin/settings/backup", "")
	if masked.Code != 200 || strings.Contains(masked.Body.String(), c.SecretKey) || strings.Contains(masked.Body.String(), c.AccessKey) || !strings.Contains(masked.Body.String(), `"secret_key_set":true`) {
		t.Fatal("credentials leaked or missing flags", masked.Body.String())
	}
	c.AccessKey = ""
	c.SecretKey = ""
	c.DailyTime = "22:40"
	c.RetentionDays = 45
	raw, _ = json.Marshal(c)
	if w := request("PUT", "/api/admin/settings/backup", string(raw)); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	persisted, err := api.backups.Config(ctx)
	if err != nil || persisted.SecretKey != "private-storage-secret" || persisted.DailyTime != "22:40" || persisted.RetentionDays != 45 {
		t.Fatal("settings failed to persist", err)
	}
	for _, query := range []string{"?page=0", "?page=1&page=2", "?page_size=101", "?other=1"} {
		if w := request("GET", "/api/admin/backups"+query, ""); w.Code != 400 {
			t.Error("invalid pagination accepted", query, w.Code)
		}
	}
	if w := request("GET", "/api/admin/backups/missing/download", ""); w.Code != 404 {
		t.Error("unknown download", w.Code)
	}
	if w := request("DELETE", "/api/admin/backups/missing", ""); w.Code != 404 {
		t.Error("unknown deletion", w.Code)
	}
	if w := request("GET", "/api/admin/backups", ""); w.Code != 200 || !strings.Contains(w.Body.String(), `"items":[]`) {
		t.Fatal("empty backup list invalid", w.Body.String())
	}
}
