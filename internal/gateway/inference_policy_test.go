package gateway

import (
	"context"
	"encoding/json"
	"fiftyapi/internal/providers"
	"fiftyapi/internal/store"
	ws "github.com/coder/websocket"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"
)

func TestInferencePolicy(t *testing.T) {
	policy := providers.InferencePolicy{MaxEffort: "medium", MaxThinkingTokens: 4096}
	for _, tc := range []struct {
		name, protocol, input, want string
		invalid                     bool
	}{
		{"responses", "responses", `{"reasoning":{"effort":"xhigh","summary":"auto"},"service_tier":"priority"}`, `{"reasoning":{"effort":"medium","summary":"auto"},"service_tier":"priority"}`, false},
		{"lower", "responses", `{"reasoning":{"effort":"low"}}`, `{"reasoning":{"effort":"low"}}`, false},
		{"default", "responses", `{}`, `{"reasoning":{"effort":"medium"}}`, false},
		{"chat", "chat", `{"reasoning_effort":"high"}`, `{"reasoning_effort":"medium"}`, false},
		{"adaptive", "messages", `{"thinking":{"type":"adaptive"},"output_config":{"effort":"max","format":{"type":"json_schema"}},"speed":"fast"}`, `{"thinking":{"type":"adaptive"},"output_config":{"effort":"medium","format":{"type":"json_schema"}},"speed":"fast"}`, false},
		{"legacy budget", "messages", `{"thinking":{"type":"enabled","budget_tokens":20000},"max_tokens":25000}`, `{"thinking":{"type":"enabled","budget_tokens":4096},"max_tokens":25000}`, false},
		{"lower budget", "messages", `{"thinking":{"type":"enabled","budget_tokens":2048}}`, `{"thinking":{"type":"enabled","budget_tokens":2048}}`, false},
		{"disabled", "messages", `{"thinking":{"type":"disabled"}}`, `{"thinking":{"type":"disabled"}}`, false},
		{"message overrides", "messages", `{"messages":[{"role":"system","content":"","output_config":{"effort":"max"}},{"role":"user","content":"hello"}]}`, `{"output_config":{"effort":"medium"},"messages":[{"role":"system","content":"","output_config":{"effort":"medium"}},{"role":"user","content":"hello"}]}`, false},
		{"unknown effort", "responses", `{"reasoning":{"effort":"future"}}`, `null`, true},
		{"malformed", "responses", `{"reasoning":[]}`, `null`, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var request map[string]json.RawMessage
			_ = json.Unmarshal([]byte(tc.input), &request)
			// Retry maps share raw byte slices; applying policy must not change them.
			original, _ := json.Marshal(request)
			attempt := make(map[string]json.RawMessage)
			for k, v := range request {
				attempt[k] = v
			}
			err := applyInferencePolicy(attempt, tc.protocol, policy)
			if (err != nil) != tc.invalid {
				t.Fatalf("error = %v", err)
			}
			after, _ := json.Marshal(request)
			if string(after) != string(original) {
				t.Fatal("mutated another attempt")
			}
			if tc.invalid {
				return
			}
			gotJSON, _ := json.Marshal(attempt)
			var got, want any
			_ = json.Unmarshal(gotJSON, &got)
			_ = json.Unmarshal([]byte(tc.want), &want)
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("got %s; want %s", gotJSON, tc.want)
			}
		})
	}
	var request map[string]json.RawMessage
	_ = json.Unmarshal([]byte(`{"service_tier":"fast"}`), &request)
	if applyInferencePolicy(request, "responses", providers.InferencePolicy{}) != nil {
		t.Fatal("pricing-independent policy rejected premium tier")
	}
}

func TestInferencePolicyWebSocket(t *testing.T) {
	g, server := websocketGateway(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("OpenAI-Service-Tier") != "fast" {
			t.Error("premium header lost")
		}
		conn, err := ws.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer conn.CloseNow()
		for {
			_, raw, err := conn.Read(r.Context())
			if err != nil {
				return
			}
			var request map[string]json.RawMessage
			_ = json.Unmarshal(raw, &request)
			var reasoning map[string]string
			_ = json.Unmarshal(request["reasoning"], &reasoning)
			if reasoning["effort"] != "medium" || string(request["service_tier"]) != `"fast"` {
				t.Errorf("WS policy escaped: %s", raw)
			}
			err = conn.Write(r.Context(), ws.MessageText, []byte(`{"type":"response.completed","response":{"id":"policy_response","status":"completed","output":[],"usage":{"input_tokens":10,"output_tokens":2}}}`))
			if err != nil {
				return
			}
		}
	})
	setTestInferencePolicy(t, g, providers.InferencePolicy{MaxEffort: "medium"})
	conn := dialGatewayWS(t, g, server)
	for _, effort := range []string{"xhigh", "max"} {
		sendWS(t, conn, `{"type":"response.create","model":"model","input":"hello","reasoning":{"effort":"`+effort+`"},"service_tier":"fast"}`)
		if event := readWSTerminal(t, conn); string(event["type"]) != `"response.completed"` {
			t.Fatalf("WS request failed: %v", event)
		}
	}
	deadline := time.Now().Add(3 * time.Second)
	for {
		logs, err := g.s.Logs(context.Background(), g.user, false, 0, "", 0)
		if err != nil {
			t.Fatal(err)
		}
		if len(logs) == 2 {
			seen := map[string]bool{}
			for _, entry := range logs {
				if d := entry.Diagnostics; d == nil || d.Protocol != "responses" || d.ClientTransport != "websocket" || d.UpstreamTransport != "websocket" || !d.Completed {
					t.Fatalf("incorrect WebSocket diagnostics: %+v", d)
				}
				if entry.ServiceTier != "fast" {
					t.Fatalf("WS requested tier lost: %+v", entry.RequestInference)
				}
				seen[entry.ReasoningEffort] = true
			}
			if !seen["xhigh"] || !seen["max"] {
				t.Fatalf("WS turn settings mixed: %v", seen)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("WS inference logs missing")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func setTestInferencePolicy(t *testing.T, g testGateway, p providers.InferencePolicy) {
	t.Helper()
	ctx := context.Background()
	p0, err := g.s.Price(ctx, "model")
	if err != nil {
		t.Fatal(err)
	}
	p0.Rates = []store.PriceRate{{Tier: "default", Input: p0.Input, Output: p0.Output, Cached: &p0.Cached, CacheWrite: &p0.CacheWrite}, {Tier: "fast", Input: 2 * p0.Input, Output: 2 * p0.Output, Cached: &p0.Cached, CacheWrite: &p0.CacheWrite}}
	if err = g.s.SetModel(ctx, g.user, "model", true, &p0); err != nil {
		t.Fatal(err)
	}
	cfg, err := g.h.accounts.RuntimeConfig(ctx, g.account)
	if err != nil {
		t.Fatal(err)
	}
	cfg.InferencePolicy = p
	cfg.GatewayHeaders = map[string]string{"OpenAI-Service-Tier": "fast"}
	if err = g.h.accounts.UpdateRuntime(ctx, g.user, g.account, cfg); err != nil {
		t.Fatal(err)
	}
	saved, err := g.h.accounts.RuntimeConfig(ctx, g.account)
	if err != nil || saved.InferencePolicy != p {
		t.Fatalf("policy round trip: %+v, %v", saved.InferencePolicy, err)
	}
	if err = g.s.AccountState(ctx, g.account, "catalog_ready", ""); err != nil {
		t.Fatal(err)
	}
}

func TestInferencePolicyForwarding(t *testing.T) {
	for _, provider := range []string{"openai", "claude", "gateway"} {
		t.Run(provider, func(t *testing.T) {
			calls := 0
			upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				var body map[string]any
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Error(err)
				}
				if provider == "openai" || provider == "gateway" {
					if body["service_tier"] != "priority" || body["reasoning"].(map[string]any)["effort"] != "medium" || r.Header.Get("OpenAI-Service-Tier") != "fast" {
						t.Errorf("policy not applied: %v", body)
					}
				} else if provider == "claude" {
					if body["speed"] != "fast" || body["service_tier"] != "priority" || body["output_config"].(map[string]any)["effort"] != "medium" {
						t.Errorf("Claude policy not applied: %v", body)
					}
				} else {
					config := body["request"].(map[string]any)["generationConfig"].(map[string]any)
					if config["thinkingConfig"].(map[string]any)["thinkingBudget"] != float64(4096) {
						t.Errorf("Antigravity budget escaped: %v", config)
					}
				}
				// A known rejection is enough to inspect outgoing policy and settlement.
				w.WriteHeader(400)
				io.WriteString(w, `{"error":"fixture"}`)
			}))
			defer upstream.Close()
			var g testGateway
			if provider == "gateway" {
				g = setupRelay(t, upstream, "custom", "responses")
			} else {
				g = setupGateway(t, provider, upstream)
			}
			setTestInferencePolicy(t, g, providers.InferencePolicy{MaxEffort: "medium", MaxThinkingTokens: 4096})
			path := "/v1/messages"
			if provider == "openai" || provider == "gateway" {
				path = "/v1/responses"
			}
			w := g.request(path, `{"model":"model","input":"hello","messages":[{"role":"user","content":"hello"}],"thinking":{"type":"adaptive"},"output_config":{"effort":"max"},"reasoning":{"effort":"xhigh"},"speed":"fast","service_tier":"priority"}`)
			if w.Code != 400 || calls != 1 {
				t.Fatalf("status %d, calls %d: %s", w.Code, calls, w.Body.String())
			}
			quota, err := g.s.Quota(context.Background(), g.user)
			if err != nil {
				t.Fatal(err)
			}
			if quota[0].Used != 0 || quota[0].Reserved != 0 {
				t.Fatalf("rejection charged quota: %+v", quota)
			}
		})
	}
}
