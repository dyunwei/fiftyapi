package accounts

import (
	"bytes"
	"context"
	"encoding/json"
	"fiftyapi/internal/providers"
	"fiftyapi/internal/store"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

func TestGatewayLifecycleDiscoveryAndKeyRotation(t *testing.T) {
	key := "first-test-key"
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			t.Error("gateway attempted OAuth or unexpected path")
			w.WriteHeader(404)
			return
		}
		if r.Header.Get("Authorization") != "Bearer "+key {
			t.Error("wrong API key used")
		}
		if r.Header.Get("chatgpt-account-id") != "" || r.Header.Get("originator") != "" {
			t.Error("OAuth identity leaked into gateway discovery")
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"data":[{"id":"model"}]}`))
	}))
	defer upstream.Close()
	ctx := context.Background()
	s, err := store.Open(ctx, filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	actor, err := s.CreateAdmin(ctx, "admin", "hash")
	if err != nil {
		t.Fatal(err)
	}
	service, err := New(s, make([]byte, 32), WithHTTPClient(upstream.Client()))
	if err != nil {
		t.Fatal(err)
	}
	cfg := providers.Config{Provider: "gateway", Gateway: &providers.GatewayConfig{Platform: "sub2api", BaseURL: upstream.URL, Protocols: []string{"messages", "responses"}, Discovery: "auto", AuthMode: "bearer"}}
	id, err := service.Import(ctx, actor, "gateway", Credential{Config: cfg, APIKey: key})
	if err != nil {
		t.Fatal(err)
	}
	names, err := service.Verify(ctx, id)
	if err != nil || len(names) != 1 {
		t.Fatalf("discovery failed: %v", err)
	}
	row, err := s.Account(ctx, id)
	if err != nil || row.Provider != "gateway" || row.Platform != "sub2api" || bytes.Contains(row.Credentials, []byte(key)) {
		t.Fatal("account metadata/encryption incorrect")
	}
	runtime, err := service.RuntimeConfig(ctx, id)
	if err != nil || runtime.APIKey != "" {
		t.Fatal("configuration exposed API key")
	}
	raw, _ := json.Marshal(runtime)
	if strings.Contains(string(raw), key) {
		t.Fatal("configuration JSON exposed key")
	}
	runtime.Gateway.Models = []providers.GatewayModel{{ID: "my-messages", Protocol: "messages", UpstreamID: "model"}}
	if err = service.UpdateRuntime(ctx, actor, id, runtime); err != nil {
		t.Fatal(err)
	}
	if _, err = service.Verify(ctx, id); err != nil {
		t.Fatal(err)
	}
	c, err := service.Access(ctx, id)
	if err != nil || c.APIKey != key {
		t.Fatal("blank replacement failed to retain key")
	}
	key = "rotated-test-key"
	runtime.APIKey = key
	if err = service.UpdateRuntime(ctx, actor, id, runtime); err != nil {
		t.Fatal(err)
	}
	if _, err = service.Verify(ctx, id); err != nil {
		t.Fatal("rotated key not used")
	}
	models, err := s.Models(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, model := range models {
		if model.Price != nil {
			t.Fatal("assumed vendor pricing seeded for gateway")
		}
	}
	runtime.Gateway.Discovery = "manual"
	runtime.Gateway.Models = []providers.GatewayModel{{ID: "manual", UpstreamID: "manual-upstream", Protocol: "messages"}}
	runtime.APIKey = ""
	if err = service.UpdateRuntime(ctx, actor, id, runtime); err != nil {
		t.Fatal(err)
	}
	upstream.Close()
	if _, err = service.Verify(ctx, id); err != nil {
		t.Fatal("manual mode attempted network discovery")
	}
}
