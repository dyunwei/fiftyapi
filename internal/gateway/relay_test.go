package gateway

import (
	"context"
	"encoding/json"
	"fiftyapi/internal/accounts"
	"fiftyapi/internal/providers"
	"fiftyapi/internal/store"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

func setupRelay(t *testing.T, upstream *httptest.Server, platform, protocol string) testGateway {
	t.Helper()
	ctx := context.Background()
	s, err := store.Open(ctx, filepath.Join(t.TempDir(), "relay.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	user, err := s.CreateAdmin(ctx, "admin", "hash")
	if err != nil {
		t.Fatal(err)
	}
	if err = s.SetLimits(ctx, user, user, 10000000, 100000000); err != nil {
		t.Fatal(err)
	}
	_, token, err := s.CreateAPIKey(ctx, user, "client")
	if err != nil {
		t.Fatal(err)
	}
	service, err := accounts.New(s, make([]byte, 32), accounts.WithHTTPClient(upstream.Client()))
	if err != nil {
		t.Fatal(err)
	}
	cfg := providers.Config{Provider: "gateway", Gateway: &providers.GatewayConfig{Platform: platform, BaseURL: upstream.URL, Protocols: []string{protocol}, Discovery: "manual", AuthMode: "bearer", Compact: true, Models: []providers.GatewayModel{{ID: "model", UpstreamID: "upstream-model", Protocol: protocol}}}}
	id, err := service.Import(ctx, user, "gateway", accounts.Credential{Config: cfg, APIKey: "upstream-private-test-key"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = service.Verify(ctx, id); err != nil {
		t.Fatal(err)
	}
	if err = s.SetModel(ctx, user, "model", true, &store.Price{Input: 1000000, Output: 1000000, Cached: 1000000, CacheWrite: 1000000}); err != nil {
		t.Fatal(err)
	}
	h := New(s, service)
	h.client = func(providers.Config) *http.Client {
		c := *upstream.Client()
		c.Transport = c.Transport.(*http.Transport).Clone()
		c.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
		return &c
	}
	return testGateway{h: h, s: s, token: token, user: user, account: id}
}
func TestGatewayRelayPassthroughAllPlatformsAndProtocols(t *testing.T) {
	for _, platform := range []string{"sub2api", "fiftyapi", "new-api"} {
		for _, protocol := range []string{"messages", "responses"} {
			t.Run(platform+"/"+protocol, func(t *testing.T) {
				var calls atomic.Int64
				upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					calls.Add(1)
					if r.URL.Path != "/v1/"+protocol {
						t.Error("wrong public endpoint")
					}
					if r.Header.Get("Authorization") != "Bearer upstream-private-test-key" {
						t.Error("caller key forwarded instead of upstream key")
					}
					if r.Header.Get("chatgpt-account-id") != "" || r.Header.Get("originator") != "" || strings.Contains(r.Header.Get("anthropic-beta"), "oauth") {
						t.Error("OAuth headers applied")
					}
					if r.Header.Get(providers.RelayChainHeader) == "" {
						t.Error("missing relay chain")
					}
					var request map[string]any
					json.NewDecoder(r.Body).Decode(&request)
					if request["model"] != "upstream-model" || request["temperature"] != 0.25 || request["store"] != true {
						t.Error("request modified beyond model mapping")
					}
					if system, ok := request["system"].(string); !ok || system != "original system" {
						t.Error("system prompt changed")
					}
					stream, _ := request["stream"].(bool)
					if stream {
						w.Header().Set("Content-Type", "text/event-stream")
						if protocol == "messages" {
							io.WriteString(w, "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"usage\":{\"input_tokens\":10,\"output_tokens\":0}}}\n\nevent: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"tool_use\",\"id\":\"tool-1\",\"name\":\"add\",\"input\":{}}}\n\nevent: message_delta\ndata: {\"type\":\"message_delta\",\"usage\":{\"output_tokens\":2}}\n\nevent: message_stop\ndata: {\"type\":\"message_stop\"}\n\n")
						} else {
							io.WriteString(w, "event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\",\"output\":[{\"type\":\"function_call\",\"name\":\"add\",\"arguments\":\"{\\\"a\\\":2,\\\"b\\\":3}\"}],\"usage\":{\"input_tokens\":10,\"output_tokens\":2}}}\n\n")
						}
						return
					}
					w.Header().Set("Content-Type", "application/json")
					if protocol == "messages" {
						io.WriteString(w, `{"type":"message","role":"assistant","content":[{"type":"tool_use","id":"tool-1","name":"add","input":{"a":2,"b":3},"signature":"preserved-test-signature"}],"usage":{"input_tokens":10,"output_tokens":2}}`)
					} else {
						io.WriteString(w, `{"status":"completed","output":[{"type":"function_call","name":"add","arguments":"{\"a\":2,\"b\":3}"}],"usage":{"input_tokens":10,"output_tokens":2}}`)
					}
				}))
				defer upstream.Close()
				g := setupRelay(t, upstream, platform, protocol)
				for _, stream := range []bool{false, true} {
					body, _ := json.Marshal(map[string]any{"model": "model", "stream": stream, "temperature": 0.25, "store": true, "system": "original system", "max_tokens": 32, "input": []any{map[string]string{"type": "function_call_output", "call_id": "tool-previous", "output": "5"}}, "messages": []any{map[string]any{"role": "user", "content": []any{map[string]string{"type": "tool_result", "tool_use_id": "tool-previous", "content": "5"}}}}})
					response := g.request("/v1/"+protocol, string(body))
					if response.Code != 200 || !strings.Contains(response.Body.String(), "add") {
						t.Fatalf("passthrough failed: %d %s", response.Code, response.Body.String())
					}
				}
				quota, err := g.s.Quota(context.Background(), g.user)
				if err != nil || quota[0].Used != 24 || quota[0].Reserved != 0 || calls.Load() != 2 {
					t.Fatal("relay billing mismatch")
				}
				logs, err := g.s.Logs(context.Background(), g.user, false, 0, "", 0)
				if err != nil || len(logs) != 2 {
					t.Fatal("missing relay logs")
				}
				for _, log := range logs {
					raw, _ := json.Marshal(log)
					if strings.Contains(string(raw), "private-test-key") || strings.Contains(string(raw), "original system") {
						t.Fatal("relay credential/prompt leaked into logs")
					}
				}
			})
		}
	}
}

func TestRelayFailureUsageAndLoopGuards(t *testing.T) {
	for _, kind := range []string{"truncated", "missing_usage", "invalid_cached", "error_object", "429", "508"} {
		t.Run(kind, func(t *testing.T) {
			var calls atomic.Int64
			upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				switch kind {
				case "429":
					w.WriteHeader(429)
				case "508":
					w.WriteHeader(508)
				case "truncated":
					w.Header().Set("Content-Type", "text/event-stream")
					io.WriteString(w, "event: response.output_text.delta\ndata: {\"delta\":\"partial\"}\n\n")
				case "missing_usage":
					io.WriteString(w, `{"status":"completed","output":[]}`)
				case "error_object":
					io.WriteString(w, `{"error":{"message":"private-content"}}`)
				default:
					io.WriteString(w, `{"status":"completed","usage":{"input_tokens":2,"output_tokens":1,"input_tokens_details":{"cached_tokens":9}}}`)
				}
			}))
			defer upstream.Close()
			g := setupRelay(t, upstream, "new-api", "responses")
			response := g.request("/v1/responses", `{"model":"model","stream":false,"input":"test"}`)
			pending, err := g.s.PendingCharges(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			quota, err := g.s.Quota(context.Background(), g.user)
			if err != nil {
				t.Fatal(err)
			}
			if len(pending) != 0 || quota[0].Used != 0 || quota[0].Reserved != 0 {
				t.Fatalf("failed/unknown usage should be zero-settled: %s status=%d", kind, response.Code)
			}
			logs, err := g.s.Logs(context.Background(), g.user, false, 0, "", 0)
			if err != nil || len(logs) != 1 || logs[0].Amount == nil || *logs[0].Amount != 0 {
				t.Fatal("missing zero settlement log", err)
			}
			if kind != "429" && kind != "508" && !strings.Contains(logs[0].Error, "按 0 结算") {
				t.Fatal("unknown usage not explained")
			}

			request := httptest.NewRequest("POST", "/v1/responses", strings.NewReader(`{"model":"model","input":"test"}`))
			request.Header.Set("Authorization", "Bearer "+g.token)
			request.Header.Set(providers.RelayChainHeader, g.h.instanceID)
			w := httptest.NewRecorder()
			g.h.Serve(w, request)
			if w.Code != 508 || calls.Load() != 1 {
				t.Fatal("cycle reached upstream")
			}
		})
	}
}

func TestRelayCompactXAPIKeyAndCodexManifest(t *testing.T) {
	var calls atomic.Int64
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Header.Get("x-api-key") != "upstream-private-test-key" || r.Header.Get("Authorization") != "" {
			t.Error("x-api-key mode not applied")
		}
		if r.Header.Get("originator") != "" || r.Header.Get("chatgpt-account-id") != "" {
			t.Error("OAuth headers on API gateway")
		}
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/v1/models" {
			io.WriteString(w, `{"models":[{"slug":"upstream-model","context_window":12345,"supports_reasoning_summaries":true}]}`)
			return
		}
		if r.URL.Path != "/v1/responses/compact" {
			t.Error("wrong compact route")
		}
		io.WriteString(w, `{"object":"response.compaction","usage":{"input_tokens":10,"output_tokens":2}}`)
	}))
	defer upstream.Close()
	g := setupRelay(t, upstream, "fiftyapi", "responses")
	cfg, err := g.h.accounts.RuntimeConfig(context.Background(), g.account)
	if err != nil {
		t.Fatal(err)
	}
	cfg.Gateway.AuthMode = "x-api-key"
	cfg.Gateway.Compact = false
	if err = g.h.accounts.UpdateRuntime(context.Background(), g.user, g.account, cfg); err != nil {
		t.Fatal(err)
	}
	if _, err = g.h.accounts.Verify(context.Background(), g.account); err != nil {
		t.Fatal(err)
	}
	if response := g.request("/v1/responses/compact", `{"model":"model","input":[]}`); response.Code != 400 || calls.Load() != 0 {
		t.Fatal("disabled compact forwarded")
	}
	cfg.Gateway.Compact = true
	if err = g.h.accounts.UpdateRuntime(context.Background(), g.user, g.account, cfg); err != nil {
		t.Fatal(err)
	}
	if _, err = g.h.accounts.Verify(context.Background(), g.account); err != nil {
		t.Fatal(err)
	}
	if response := g.request("/v1/responses/compact", `{"model":"model","input":[]}`); response.Code != 200 {
		t.Fatal("compact failed")
	}
	request := httptest.NewRequest("GET", "/v1/models?client_version=0.146.0", nil)
	request.Header.Set("Authorization", "Bearer "+g.token)
	w := httptest.NewRecorder()
	g.h.Models(w, request)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"slug":"model"`) || !strings.Contains(w.Body.String(), `"context_window":12345`) {
		t.Fatalf("manifest metadata lost: %d %s", w.Code, w.Body.String())
	}
}

func TestRelayDoesNotFollowCredentialRedirect(t *testing.T) {
	var received atomic.Int64
	target := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { received.Add(1) }))
	defer target.Close()
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL+"/collect", http.StatusTemporaryRedirect)
	}))
	defer upstream.Close()
	g := setupRelay(t, upstream, "custom", "messages")
	response := g.request("/v1/messages", `{"model":"model","max_tokens":32,"messages":[{"role":"user","content":"test"}]}`)
	if response.Code != 502 || received.Load() != 0 {
		t.Fatal("gateway followed redirect and exposed credentials")
	}
}

func TestModelDirectoryAdvertisesActualProtocol(t *testing.T) {
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { t.Error("listing unexpectedly called upstream") }))
	defer upstream.Close()
	for _, protocol := range []string{"messages", "responses"} {
		g := setupRelay(t, upstream, "new-api", protocol)
		r := httptest.NewRequest("GET", "/v1/models", nil)
		r.Header.Set("Authorization", "Bearer "+g.token)
		w := httptest.NewRecorder()
		g.h.Models(w, r)
		var directory struct {
			Data []struct {
				ID       string   `json:"id"`
				Protocol string   `json:"protocol"`
				Types    []string `json:"supported_endpoint_types"`
				Paths    []string `json:"supported_endpoints"`
			} `json:"data"`
		}
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &directory) != nil || len(directory.Data) != 1 {
			t.Fatal("model directory malformed")
		}
		model := directory.Data[0]
		want := "anthropic"
		if protocol == "responses" {
			want = "openai-response"
		}
		if model.ID != "model" || model.Protocol != protocol || len(model.Types) == 0 || model.Types[0] != want || len(model.Paths) == 0 || model.Paths[0] != "/v1/"+protocol {
			t.Fatal("directory advertised incorrect protocol or changed model ID")
		}
		if protocol == "messages" && (len(model.Types) != 1 || len(model.Paths) != 1) {
			t.Fatal("Messages model incorrectly advertises Chat Completions")
		}
		if protocol == "responses" && (len(model.Types) != 1 || len(model.Paths) != 1) {
			t.Fatal("Responses model advertised unimplemented Chat support")
		}
	}
}
