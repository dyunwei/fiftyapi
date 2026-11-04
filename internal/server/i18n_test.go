package server

import (
	"context"
	"fiftyapi/internal/store"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

func TestAPIErrorLanguageAndOriginBoundary(t *testing.T) {
	s, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	a, err := New(s, Config{})
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ language, want, tag string }{
		{"en-US", "Please sign in first", "en"},
		{"zh-CN", "请先登录", "zh-CN"},
		{"", "请先登录", "zh-CN"},
	} {
		req := httptest.NewRequest("GET", "http://example.test/api/me", nil)
		req.Header.Set("Accept-Language", tc.language)
		out := httptest.NewRecorder()
		a.Handler().ServeHTTP(out, req)
		if out.Code != 401 || !strings.Contains(out.Body.String(), tc.want) || out.Header().Get("Content-Language") != tc.tag || !strings.Contains(out.Header().Get("Vary"), "Accept-Language") {
			t.Fatalf("language %q: %d %s %v", tc.language, out.Code, out.Body.String(), out.Header())
		}
	}
	// Early same-origin rejection must also be localized and retain its status.
	req := httptest.NewRequest("POST", "http://example.test/api/setup", strings.NewReader(`{}`))
	req.Header.Set("Origin", "https://other.test")
	req.Header.Set("Accept-Language", "en")
	out := httptest.NewRecorder()
	a.Handler().ServeHTTP(out, req)
	if out.Code != 403 || !strings.Contains(out.Body.String(), "Request origin mismatch") {
		t.Fatalf("origin boundary: %d %s", out.Code, out.Body.String())
	}
}
