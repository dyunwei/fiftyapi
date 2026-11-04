package providers

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestGatewayBaseValidationAndRelayChain(t *testing.T) {
	for _, item := range []struct{ raw, want string }{{"https://example.test", "https://example.test/v1"}, {"https://example.test/", "https://example.test/v1"}, {"https://example.test/v1/", "https://example.test/v1"}, {"https://example.test/prefix/", "https://example.test/prefix/v1"}} {
		base, err := NormalizeGatewayBase(item.raw, false)
		if err != nil || base != item.want {
			t.Fatalf("base normalization: %q %v", base, err)
		}
	}
	for _, raw := range []string{"http://example.test", "https://key@example.test", "https://example.test?key=private", "https://example.test/#fragment", "https://example.test/v1/messages", "https://example.test/a/../v1", "https://example.test//v1", "https://example.test/%2fsecret"} {
		if _, err := NormalizeGatewayBase(raw, false); err == nil {
			t.Fatal("unsafe/ambiguous base allowed")
		}
	}
	if _, err := NormalizeGatewayBase("http://internal:8080/v1", true); err != nil {
		t.Fatal("explicit trusted HTTP blocked")
	}
	id := strings.Repeat("a", 32)
	chain, err := NextRelayChain("", id)
	if err != nil || chain != id {
		t.Fatal("initial chain failed")
	}
	if _, err = NextRelayChain(chain, id); err == nil {
		t.Fatal("cycle allowed")
	}
	if _, err = NextRelayChain(strings.Join([]string{strings.Repeat("a", 32), strings.Repeat("b", 32), strings.Repeat("c", 32), strings.Repeat("d", 32)}, ","), strings.Repeat("e", 32)); err == nil {
		t.Fatal("hop limit missing")
	}
	if _, err = NextRelayChain("untrusted-value", id); err == nil {
		t.Fatal("malformed chain accepted")
	}
}

func TestGatewayDiscoveryPaginationUsesOnlyConfiguredOrigin(t *testing.T) {
	var requests int
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.Header.Get("Authorization") != "Bearer opaque-test-key" || r.Header.Get(RelayChainHeader) != strings.Repeat("a", 32) {
			t.Error("discovery authentication/chain missing")
		}
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Query().Get("after_id") == "" {
			io.WriteString(w, `{"data":[{"id":"one"}],"has_more":true,"last_id":"one","next":"https://untrusted.example/collect"}`)
		} else if r.URL.Query().Get("after_id") == "one" {
			io.WriteString(w, `{"data":[{"id":"two"}],"has_more":false}`)
		} else {
			t.Error("unexpected cursor")
		}
	}))
	defer upstream.Close()
	c := Config{Provider: "gateway", Gateway: &GatewayConfig{Platform: "sub2api", BaseURL: upstream.URL, Protocols: []string{"messages"}, Discovery: "auto", AuthMode: "bearer"}}
	models, err := c.GatewayModels(context.Background(), upstream.Client(), "opaque-test-key", strings.Repeat("a", 32))
	if err != nil || len(models) != 2 || requests != 2 {
		t.Fatalf("pagination failed: %v", err)
	}
	c.GatewayHeaders = map[string]string{RelayChainHeader: "override"}
	if c.Validate() == nil {
		t.Fatal("custom relay-chain override accepted")
	}
}

func TestExplicitModelProtocolsOverrideDiscoveryDefaults(t *testing.T) {
	up := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"data":[{"id":"a"},{"id":"b"}]}`))
	}))
	defer up.Close()
	cfg := Config{Provider: "gateway", Gateway: &GatewayConfig{Platform: "new-api", BaseURL: up.URL, AuthMode: "bearer", Discovery: "auto", Protocols: []string{"chat", "responses"}, Models: []GatewayModel{{ID: "only-chat", UpstreamID: "a", Protocols: []string{"chat"}}}}}
	if e := cfg.Validate(); e != nil {
		t.Fatal(e)
	}
	models, e := cfg.GatewayModels(context.Background(), up.Client(), "fixture", "chain")
	if e != nil || len(models) != 2 {
		t.Fatal(e)
	}
	for _, m := range models {
		if m.UpstreamID == "a" && (len(m.SupportedProtocols()) != 1 || m.SupportedProtocols()[0] != "chat") {
			t.Fatal("override incorrectly added to defaults")
		}
		if m.UpstreamID == "b" && len(m.SupportedProtocols()) != 2 {
			t.Fatal("default protocols lost")
		}
	}
	cfg.Gateway.Models[0].Protocols = []string{"chat", "chat"}
	if cfg.Validate() == nil {
		t.Fatal("duplicate protocol accepted")
	}
}
