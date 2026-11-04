package server

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fiftyapi/internal/accounts"
	"fiftyapi/internal/providers"
	"fiftyapi/internal/store"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestGatewayAdminBoundaryAndWriteOnlyAPIKey(t *testing.T) {
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
	api, err := New(s, Config{})
	if err != nil {
		t.Fatal(err)
	}
	service, err := accounts.New(s, make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	api.AttachAccounts(service)
	for _, who := range []struct {
		token string
		user  int64
	}{{strings.Repeat("a", 43), admin}, {strings.Repeat("b", 43), member}} {
		hash := sha256.Sum256([]byte(who.token))
		if err = s.SaveSession(ctx, hash[:], who.user, "csrf", time.Now().Add(time.Hour)); err != nil {
			t.Fatal(err)
		}
	}
	request := func(method, path, body, token string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, "http://example.test"+path, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("X-CSRF-Token", "csrf")
		if token != "" {
			r.AddCookie(&http.Cookie{Name: cookieName, Value: token})
		}
		w := httptest.NewRecorder()
		api.Handler().ServeHTTP(w, r)
		return w
	}
	payload, _ := json.Marshal(map[string]any{"inference_policy": providers.InferencePolicy{MaxEffort: "medium", ForceStandard: true}, "name": "gateway", "api_key": "private-server-test-key", "gateway": providers.GatewayConfig{Platform: "new-api", BaseURL: "https://upstream.example", Protocols: []string{"messages"}, Discovery: "manual", AuthMode: "bearer", Models: []providers.GatewayModel{{ID: "relay-model", UpstreamID: "upstream-model", Protocol: "messages"}}}})
	if w := request("POST", "/api/admin/gateway-accounts", string(payload), ""); w.Code != 401 {
		t.Fatal("anonymous gateway creation allowed")
	}
	if w := request("POST", "/api/admin/gateway-accounts", string(payload), strings.Repeat("b", 43)); w.Code != 403 {
		t.Fatal("member gateway creation allowed")
	}
	response := request("POST", "/api/admin/gateway-accounts", string(payload), strings.Repeat("a", 43))
	if response.Code != 201 || strings.Contains(response.Body.String(), "private-server-test-key") {
		t.Fatalf("gateway creation failed or leaked key: HTTP %d", response.Code)
	}
	var created struct{ ID int64 }
	json.Unmarshal(response.Body.Bytes(), &created)
	config := request("GET", fmt.Sprintf("/api/admin/accounts/%d/config", created.ID), "", strings.Repeat("a", 43))
	if config.Code != 200 || strings.Contains(config.Body.String(), "private-server-test-key") || strings.Contains(config.Body.String(), `"api_key"`) {
		t.Fatal("stored key exposed in configuration")
	}
	var saved accounts.RuntimeConfig
	if err := json.Unmarshal(config.Body.Bytes(), &saved); err != nil || saved.InferencePolicy.MaxEffort != "medium" || !saved.InferencePolicy.ForceStandard {
		t.Fatalf("creation lost inference policy: %s", config.Body.String())
	}
	list := request("GET", "/api/admin/accounts", "", strings.Repeat("a", 43))
	if strings.Contains(list.Body.String(), "private-server-test-key") || !strings.Contains(list.Body.String(), `"gateway_platform":"new-api"`) {
		t.Fatal("account metadata unsafe or missing platform")
	}
}
