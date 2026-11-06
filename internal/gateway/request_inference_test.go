package gateway

import (
	"context"
	"encoding/json"
	"fiftyapi/internal/providers"
	"fiftyapi/internal/store"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
)

func TestRequestInference(t *testing.T) {
	budget := int64(8192)
	for _, tc := range []struct {
		name, protocol, body string
		want                 store.RequestInference
	}{
		{"codex", "responses", `{"reasoning":{"effort":"xhigh"},"service_tier":"priority"}`, store.RequestInference{ReasoningEffort: "xhigh", ServiceTier: "priority"}},
		{"chat", "chat", `{"reasoning_effort":"high","service_tier":"flex"}`, store.RequestInference{ReasoningEffort: "high", ServiceTier: "flex"}},
		{"claude adaptive", "messages", `{"thinking":{"type":"adaptive"},"output_config":{"effort":"max"},"speed":"fast","service_tier":"auto"}`, store.RequestInference{ReasoningEffort: "max", ThinkingType: "adaptive", Speed: "fast", ServiceTier: "auto"}},
		{"claude budget", "messages", `{"thinking":{"type":"enabled","budget_tokens":8192}}`, store.RequestInference{ThinkingType: "enabled", ThinkingBudgetTokens: &budget}},
		{"claude disabled", "messages", `{"thinking":{"type":"disabled"}}`, store.RequestInference{ThinkingType: "disabled"}},
		{"unspecified", "responses", `{}`, store.RequestInference{}},
		{"null", "messages", `{"thinking":null,"output_config":null,"speed":null}`, store.RequestInference{}},
		{"malformed", "messages", `{"thinking":[],"output_config":{"effort":42},"speed":{},"service_tier":true}`, store.RequestInference{}},
		{"no arbitrary text", "responses", `{"reasoning":{"effort":"private prompt text"},"service_tier":"private\ntext"}`, store.RequestInference{}},
		{"invalid budget", "messages", `{"thinking":{"budget_tokens":-1}}`, store.RequestInference{}},
		{"fractional budget", "messages", `{"thinking":{"budget_tokens":1.5}}`, store.RequestInference{}},
		{"protocol isolation", "responses", `{"reasoning_effort":"high","output_config":{"effort":"max"},"thinking":{"type":"enabled"},"speed":"fast"}`, store.RequestInference{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var request map[string]json.RawMessage
			if err := json.Unmarshal([]byte(tc.body), &request); err != nil {
				t.Fatal(err)
			}
			before, _ := json.Marshal(request)
			if got := requestInference(request, tc.protocol); !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("got %+v, want %+v", got, tc.want)
			}
			after, _ := json.Marshal(request)
			if string(before) != string(after) {
				t.Fatal("logging changed request")
			}
		})
	}
}

func TestGatewayLogsRequestedInferenceBeforePolicy(t *testing.T) {
	for _, provider := range []string{"openai", "claude"} {
		t.Run(provider, func(t *testing.T) {
			upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var request map[string]json.RawMessage
				if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
					t.Error(err)
				}
				protocol, tier := "responses", "default"
				if provider == "claude" {
					protocol, tier = "messages", "standard_only"
				}
				got := requestInference(request, protocol)
				if got.ReasoningEffort != "medium" || got.ServiceTier != tier || got.Speed != "" {
					t.Errorf("policy not applied: %+v", got)
				}
				w.Header().Set("Content-Type", "application/json")
				io.WriteString(w, `{"id":"response-test","status":"completed","content":[],"output":[],"usage":{"input_tokens":10,"output_tokens":2}}`)
			}))
			defer upstream.Close()
			g := setupGateway(t, provider, upstream)
			setTestInferencePolicy(t, g, providers.InferencePolicy{MaxEffort: "medium", ForceStandard: true})
			path, body := "/v1/responses", `{"model":"model","input":"hello","reasoning":{"effort":"xhigh"},"service_tier":"priority"}`
			want := store.RequestInference{ReasoningEffort: "xhigh", ServiceTier: "priority"}
			if provider == "claude" {
				path, body = "/v1/messages", `{"model":"model","messages":[{"role":"user","content":"hello"}],"thinking":{"type":"adaptive"},"output_config":{"effort":"max"},"speed":"fast"}`
				want = store.RequestInference{ReasoningEffort: "max", ThinkingType: "adaptive", Speed: "fast"}
			}
			response := g.request(path, body)
			if response.Code != 200 {
				t.Fatalf("status %d: %s", response.Code, response.Body.String())
			}
			logs, err := g.s.Logs(context.Background(), g.user, false, 0, "", 0)
			if err != nil || len(logs) != 1 {
				t.Fatalf("logs: %v, %v", logs, err)
			}
			if !reflect.DeepEqual(logs[0].RequestInference, want) {
				t.Fatalf("logged policy output instead of request: %+v", logs[0].RequestInference)
			}
			// Rejected requests retain their settings through the early-log path.
			response = g.request(path, body[:len(body)-1]+`,"max_output_tokens":65537,"max_tokens":65537}`)
			if response.Code != 400 {
				t.Fatalf("expected rejected request, got %d", response.Code)
			}
			page, err := g.s.LogListPage(context.Background(), store.LogFilter{User: g.user, RequestID: response.Header().Get("X-Request-ID")}, 1, 20)
			if err != nil || len(page.Items) != 1 || !reflect.DeepEqual(page.Items[0].RequestInference, want) {
				t.Fatalf("early log: %+v, %v", page, err)
			}
		})
	}
}
