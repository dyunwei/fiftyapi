package server

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fiftyapi/internal/accounts"
	"fiftyapi/internal/store"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestKeyDeletionAndCatalogPagingHTTPBoundary(t *testing.T) {
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
	a, err := New(s, Config{})
	if err != nil {
		t.Fatal(err)
	}
	svc, err := accounts.New(s, make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	a.AttachAccounts(svc)
	for i, user := range []int64{admin, member} {
		token := strings.Repeat(string(rune('a'+i)), 43)
		hash := sha256.Sum256([]byte(token))
		if err = s.SaveSession(ctx, hash[:], user, "csrf", time.Now().Add(time.Hour)); err != nil {
			t.Fatal(err)
		}
	}
	request := func(method, path, token, csrf string, body ...string) *httptest.ResponseRecorder {
		payload := ""
		if len(body) > 0 {
			payload = body[0]
		}
		r := httptest.NewRequest(method, path, strings.NewReader(payload))
		r.Header.Set("Content-Type", "application/json")
		if token != "" {
			r.AddCookie(&http.Cookie{Name: cookieName, Value: token})
		}
		r.Header.Set("X-CSRF-Token", csrf)
		w := httptest.NewRecorder()
		a.Handler().ServeHTTP(w, r)
		return w
	}
	key, _, err := s.CreateAPIKey(ctx, admin, "admin-only")
	if err != nil {
		t.Fatal(err)
	}
	path := fmt.Sprintf("/api/keys/%d", key.ID)
	adminToken, memberToken := strings.Repeat("a", 43), strings.Repeat("b", 43)
	if w := request("DELETE", path, "", ""); w.Code != 401 {
		t.Fatal("anonymous deletion")
	}
	if w := request("DELETE", path, adminToken, ""); w.Code != 403 {
		t.Fatal("delete missing csrf")
	}
	if w := request("DELETE", path, memberToken, "csrf"); w.Code != 404 {
		t.Fatal("cross-user deletion")
	}
	if w := request("POST", path+"/revoke", adminToken, "csrf"); w.Code != 200 {
		t.Fatal("revoke route", w.Code)
	}
	if w := request("DELETE", path, adminToken, "csrf"); w.Code != 200 {
		t.Fatal("delete route", w.Code)
	}
	if w := request("GET", "/api/keys", adminToken, ""); strings.Contains(w.Body.String(), "admin-only") {
		t.Fatal("deleted key exposed")
	}
	for _, p := range []string{"/v1/chat/completions", "/chat/completions"} {
		if w := request("POST", p, "", ""); w.Code != 401 {
			t.Fatal("chat route missing or unauthenticated", p, w.Code)
		}
	}
	for _, query := range []string{"page=0", "page_size=101", "actor_id=bad", "target_id=-1", "since=200&until=100"} {
		if w := request("GET", "/api/admin/audit-logs?"+query, adminToken, ""); w.Code != 400 {
			t.Fatal("invalid audit filter", query, w.Code)
		}
	}
	if w := request("GET", "/api/admin/audit-logs?page=1", memberToken, ""); w.Code != 403 {
		t.Fatal("member accessed audit", w.Code)
	}
	wAudit := request("GET", "/api/admin/audit-logs?page=1&action=not-found", adminToken, "")
	var auditPage store.AuditPage
	if wAudit.Code != 200 || json.Unmarshal(wAudit.Body.Bytes(), &auditPage) != nil || auditPage.Total != 0 || auditPage.PageSize != 10 || auditPage.Items == nil {
		t.Fatal("audit page malformed", wAudit.Body.String())
	}
	for _, q := range []string{"page=0&page_size=20", "page=1&page_size=101", "page=invalid&page_size=20", "protocol=invalid", "status=invalid", "account_id=0", "account_id=bad"} {
		if w := request("GET", "/api/admin/models?"+q, adminToken, ""); w.Code != 400 {
			t.Fatal("invalid page accepted")
		}
	}
	if w := request("GET", "/api/admin/models?page=1&page_size=20", memberToken, ""); w.Code != 403 {
		t.Fatal("member accessed admin catalog")
	}
	if w := request("GET", "/api/models?account_id=1", memberToken, ""); w.Code != 403 {
		t.Fatal("member accessed account filter", w.Code)
	}
	defaultPage := request("GET", "/api/admin/models?protocol=messages", adminToken, "")
	var defaults store.ModelPage
	if defaultPage.Code != 200 || json.Unmarshal(defaultPage.Body.Bytes(), &defaults) != nil || defaults.PageSize != 10 {
		t.Fatal("default model page size", defaultPage.Code, defaultPage.Body.String())
	}
	w := request("GET", "/api/models?page=1&page_size=20&q=not-found", memberToken, "")
	var page store.ModelPage
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &page) != nil || page.Total != 0 || page.Items == nil {
		t.Fatal("paged public catalog malformed")
	}
	w = request("GET", "/api/models", memberToken, "")
	var legacy []store.Model
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &legacy) != nil {
		t.Fatal("legacy catalog shape changed")
	}
	if err = s.SetLimits(ctx, admin, admin, 1000, 1000); err != nil {
		t.Fatal(err)
	}
	billingKey, _, err := s.CreateAPIKey(ctx, admin, "billing")
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Reserve(ctx, "http-pending", admin, billingKey.ID, "model", store.Price{Input: 1000000, Output: 1000000}, 100, time.Now()); err != nil {
		t.Fatal(err)
	}
	if err = s.Pending(ctx, "http-pending"); err != nil {
		t.Fatal(err)
	}
	input, output := int64(5), int64(2)
	if err = s.LogRequest(ctx, store.RequestLog{ID: "http-pending", UserID: admin, KeyID: billingKey.ID, Model: "model", Provider: "antigravity", Status: 502, Input: &input, Output: &output, Error: "结算写入失败", CreatedAt: time.Now().Unix()}); err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{"page=0", "page_size=101", "page=bad", "page=1&status=invalid", "page=1&settlement=invalid", "page=1&until=bad", "page=1&until=-1", "page=1&since=200&until=100"} {
		if w := request("GET", "/api/admin/logs?"+q, adminToken, ""); w.Code != 400 {
			t.Fatal("invalid log query accepted", q, w.Code)
		}
	}
	if w := request("GET", "/api/logs?page=1&settlement=pending", memberToken, ""); w.Code != 403 {
		t.Fatal("member accessed admin exception filter")
	}
	w = request("GET", "/api/logs?page=1&page_size=20&request_id=http-pending", memberToken, "")
	var logPage store.LogPage
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &logPage) != nil || logPage.Total != 0 {
		t.Fatal("member log isolation failed")
	}
	w = request("GET", "/api/admin/logs?page=1&page_size=20&settlement=pending", adminToken, "")
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &logPage) != nil || logPage.Total != 1 || logPage.Items[0].SettlementStatus != "pending" {
		t.Fatal("exception filter failed")
	}
	detail, retry := "/api/admin/logs/http-pending/settlement", "/api/admin/logs/http-pending/retry-settlement"
	if w := request("GET", detail, memberToken, ""); w.Code != 403 {
		t.Fatal("member viewed financial details")
	}
	if w := request("POST", retry, adminToken, ""); w.Code != 403 {
		t.Fatal("retry missing CSRF accepted")
	}
	if w := request("POST", retry, memberToken, "csrf"); w.Code != 403 {
		t.Fatal("member retried billing")
	}
	if w := request("GET", detail, adminToken, ""); w.Code != 200 || !strings.Contains(w.Body.String(), `"price_valid":true`) {
		t.Fatal("settlement details failed")
	}
	if w := request("POST", retry, adminToken, "csrf"); w.Code != 200 {
		t.Fatal("retry endpoint failed")
	}
	q, err := s.Quota(ctx, admin)
	if err != nil || q[0].Used != 7 || q[0].Reserved != 0 {
		t.Fatal("HTTP retry billing mismatch", err, q)
	}
	if err = s.Reserve(ctx, "http-manual", admin, billingKey.ID, "model", store.Price{}, 100, time.Now()); err != nil {
		t.Fatal(err)
	}
	if err = s.Pending(ctx, "http-manual"); err != nil {
		t.Fatal(err)
	}
	if w := request("POST", "/api/admin/logs/http-manual/settle", adminToken, "csrf", `{"amount":null,"reason":"确认释放预占"}`); w.Code != 400 {
		t.Fatal("null amount silently released reservation")
	}

	if w := request("POST", "/api/admin/logs/http-manual/settle", adminToken, "csrf", `{"amount":3,"reason":"核查用量后结算"}`); w.Code != 200 {
		t.Fatal("manual endpoint failed", w.Body.String())
	}

}
