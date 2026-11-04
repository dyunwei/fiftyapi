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

func TestRuntimeLogsAdminBoundaryAndValidation(t *testing.T) {
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
	member, err := s.CreateUser(ctx, admin, "member", "hash", 100, 100)
	if err != nil {
		t.Fatal(err)
	}
	for i, id := range []int64{admin, member} {
		token := strings.Repeat(string(rune('a'+i)), 43)
		hash := sha256.Sum256([]byte(token))
		if err = s.SaveSession(ctx, hash[:], id, "csrf", time.Now().Add(time.Hour)); err != nil {
			t.Fatal(err)
		}
	}
	s.RecordRuntime(ctx, store.RuntimeLog{Level: "error", Module: "accounts", Event: "model_sync", AccountID: 7, Message: "模型同步失败"})
	api, err := New(s, Config{})
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		query, token string
		want         int
	}{
		{"", "", 401}, {"", strings.Repeat("b", 43), 403}, {"", strings.Repeat("a", 43), 200},
		{"?page=0", strings.Repeat("a", 43), 400}, {"?page=1&page=2", strings.Repeat("a", 43), 400},
		{"?page_size=101", strings.Repeat("a", 43), 400}, {"?level=debug", strings.Repeat("a", 43), 400},
		{"?account_id=-1", strings.Repeat("a", 43), 400}, {"?since=200&until=100", strings.Repeat("a", 43), 400},
		{"?unknown=x", strings.Repeat("a", 43), 400}, {"?level=error&module=accounts&account_id=7", strings.Repeat("a", 43), 200},
	} {
		req := httptest.NewRequest("GET", "/api/admin/runtime-logs"+tc.query, nil)
		if tc.token != "" {
			req.AddCookie(&http.Cookie{Name: cookieName, Value: tc.token})
		}
		w := httptest.NewRecorder()
		api.Handler().ServeHTTP(w, req)
		if w.Code != tc.want {
			t.Errorf("%s: got %d want %d", tc.query, w.Code, tc.want)
		}
		if tc.want == 200 {
			var result store.RuntimeLogPage
			if json.Unmarshal(w.Body.Bytes(), &result) != nil || result.Total != 1 || result.Items[0].AccountID != 7 {
				t.Fatal("missing runtime log", w.Body.String())
			}
		}
	}
}

func TestMorningBatchPersistsLifecycleWithoutAccounts(t *testing.T) {
	s, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	api, err := New(s, Config{})
	if err != nil {
		t.Fatal(err)
	}
	api.runMorningProbeBatch(context.Background())
	result, err := s.RuntimeLogs(context.Background(), store.RuntimeLogFilter{Module: "scheduler"}, 1, 20)
	if err != nil || result.Total != 2 {
		t.Fatalf("batch lifecycle missing: %+v %v", result, err)
	}
}
