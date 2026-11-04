package gateway

import (
	"context"
	"encoding/json"
	"fiftyapi/internal/providers"
	ws "github.com/coder/websocket"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
)

func TestInferencePolicy(t *testing.T) {
	policy := providers.InferencePolicy{MaxEffort: "medium", MaxThinkingTokens: 4096, ForceStandard: true}
	for _, tc := range []struct {
		name, protocol, input, want string
		invalid                     bool
	}{
		{"responses", "responses", `{"reasoning":{"effort":"xhigh","summary":"auto"},"service_tier":"priority"}`, `{"reasoning":{"effort":"medium","summary":"auto"},"service_tier":"default"}`, false},
		{"lower", "responses", `{"reasoning":{"effort":"low"}}`, `{"reasoning":{"effort":"low"},"service_tier":"default"}`, false},
		{"default", "responses", `{}`, `{"reasoning":{"effort":"medium"},"service_tier":"default"}`, false},
		{"chat", "chat", `{"reasoning_effort":"high"}`, `{"reasoning_effort":"medium","service_tier":"default"}`, false},
		{"adaptive", "messages", `{"thinking":{"type":"adaptive"},"output_config":{"effort":"max","format":{"type":"json_schema"}},"speed":"fast"}`, `{"thinking":{"type":"adaptive"},"output_config":{"effort":"medium","format":{"type":"json_schema"}},"service_tier":"standard_only"}`, false},
		{"legacy budget", "messages", `{"thinking":{"type":"enabled","budget_tokens":20000},"max_tokens":25000}`, `{"thinking":{"type":"enabled","budget_tokens":4096},"max_tokens":25000,"service_tier":"standard_only"}`, false},
		{"lower budget", "messages", `{"thinking":{"type":"enabled","budget_tokens":2048}}`, `{"thinking":{"type":"enabled","budget_tokens":2048},"service_tier":"standard_only"}`, false},
		{"disabled", "messages", `{"thinking":{"type":"disabled"}}`, `{"thinking":{"type":"disabled"},"service_tier":"standard_only"}`, false},
		{"message overrides", "messages", `{"messages":[{"role":"system","content":"","output_config":{"effort":"max"}},{"role":"user","content":"hello"}]}`, `{"output_config":{"effort":"medium"},"service_tier":"standard_only","messages":[{"role":"system","content":"","output_config":{"effort":"medium"}},{"role":"user","content":"hello"}]}`, false},
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
			err := applyInferencePolicy(attempt, tc.protocol, policy, false)
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
	if applyInferencePolicy(request, "responses", providers.InferencePolicy{}, false) == nil {
		t.Fatal("unconfigured account accepted premium tier")
	}
}

func TestInferencePolicyWebSocket(t *testing.T) {
	g, server := websocketGateway(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("OpenAI-Service-Tier") != "" {
			t.Error("premium header survived")
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
			if reasoning["effort"] != "medium" || string(request["service_tier"]) != `"default"` {
				t.Errorf("WS policy escaped: %s", raw)
			}
			err = conn.Write(r.Context(), ws.MessageText, []byte(`{"type":"response.completed","response":{"id":"policy_response","status":"completed","output":[],"usage":{"input_tokens":10,"output_tokens":2}}}`))
			if err != nil {
				return
			}
		}
	})
	setTestInferencePolicy(t, g, providers.InferencePolicy{MaxEffort: "medium", ForceStandard: true})
	conn := dialGatewayWS(t, g, server)
	for _, effort := range []string{"xhigh", "max"} {
		sendWS(t, conn, `{"type":"response.create","model":"model","input":"hello","reasoning":{"effort":"`+effort+`"},"service_tier":"fast"}`)
		if event := readWSTerminal(t, conn); string(event["type"]) != `"response.completed"` {
			t.Fatalf("WS request failed: %v", event)
		}
	}
}

func setTestInferencePolicy(t *testing.T, g testGateway, p providers.InferencePolicy) {
	t.Helper()
	ctx := context.Background()
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
	for _, provider := range []string{"openai", "claude", "antigravity", "gateway"} {
		t.Run(provider, func(t *testing.T) {
			calls := 0
			upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				var body map[string]any
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Error(err)
				}
				if provider == "openai" || provider == "gateway" {
					if body["service_tier"] != "default" || body["reasoning"].(map[string]any)["effort"] != "medium" || r.Header.Get("OpenAI-Service-Tier") != "" {
						t.Errorf("policy not applied: %v", body)
					}
				} else if provider == "claude" {
					if body["speed"] != nil || body["service_tier"] != "standard_only" || body["output_config"].(map[string]any)["effort"] != "medium" {
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
			setTestInferencePolicy(t, g, providers.InferencePolicy{MaxEffort: "medium", MaxThinkingTokens: 4096, ForceStandard: true})
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
