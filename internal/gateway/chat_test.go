package gateway

import (
	"context"
	"encoding/json"
	"fiftyapi/internal/providers"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func chatRequest(g testGateway, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(body))
	r.Header.Set("Authorization", "Bearer "+g.token)
	w := httptest.NewRecorder()
	g.h.ChatCompletions(w, r)
	return w
}
func TestNativeChatPreservesFieldsResponseAndAccounting(t *testing.T) {
	response := `{"id":"chat-1","object":"chat.completion","choices":[{"message":{"role":"assistant","reasoning_content":"retained","tool_calls":[{"id":"call","type":"function","function":{"name":"add","arguments":"{}"}}]},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":10,"completion_tokens":2,"prompt_tokens_details":{"cached_tokens":4}}}`
	up := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			t.Error("Chat converted to another protocol")
		}
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body["model"] != "upstream-model" || body["n"] != float64(2) || body["reasoning_effort"] != "high" || body["response_format"] == nil || body["parallel_tool_calls"] != false {
			t.Error("native Chat fields altered")
		}
		if _, ok := body["system"]; ok {
			t.Error("Messages shape injected")
		}
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, response)
	}))
	defer up.Close()
	g := setupRelay(t, up, "new-api", "chat")
	w := chatRequest(g, `{"model":"model","messages":[{"role":"developer","content":"keep me"},{"role":"user","content":"hello"}],"n":2,"parallel_tool_calls":false,"reasoning_effort":"high","response_format":{"type":"json_object"}}`)
	if w.Code != 200 || w.Body.String() != response {
		t.Fatalf("native Chat passthrough failed: %d", w.Code)
	}
	quota, e := g.s.Quota(context.Background(), g.user)
	if e != nil || quota[0].Used != 12 || quota[0].Reserved != 0 {
		t.Fatal("native Chat billing mismatch")
	}
}
func TestNativeChatStreamingUsageAndTruncation(t *testing.T) {
	for _, mode := range []string{"complete", "truncated", "invalid-usage"} {
		t.Run(mode, func(t *testing.T) {
			up := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var body map[string]any
				_ = json.NewDecoder(r.Body).Decode(&body)
				options, _ := body["stream_options"].(map[string]any)
				if options["include_usage"] != true {
					t.Error("billing usage not requested")
				}
				w.Header().Set("Content-Type", "text/event-stream")
				io.WriteString(w, "data: {\"object\":\"chat.completion.chunk\",\"choices\":[{\"delta\":{\"content\":\"NATIVE_CHAT\",\"reasoning_content\":\"retained\"},\"index\":0}]}\n\n")
				usage := `{"prompt_tokens":10,"completion_tokens":2,"prompt_tokens_details":{"cached_tokens":4}}`
				if mode == "invalid-usage" {
					usage = `{"prompt_tokens":-10,"completion_tokens":2}`
				}
				io.WriteString(w, "data: {\"choices\":[],\"usage\":"+usage+"}\n\n")
				if mode != "truncated" {
					io.WriteString(w, "data: [DONE]\n\n")
				}
			}))
			defer up.Close()
			g := setupRelay(t, up, "new-api", "chat")
			w := chatRequest(g, `{"model":"model","stream":true,"stream_options":{"include_usage":false},"messages":[{"role":"user","content":"hi"}]}`)
			if w.Code != 200 || !strings.Contains(w.Body.String(), "NATIVE_CHAT") || strings.Contains(w.Body.String(), "message_start") {
				t.Fatal("Chat SSE was converted")
			}
			if (mode == "truncated") == strings.Contains(w.Body.String(), "[DONE]") {
				t.Fatal("completion marker mishandled")
			}
			q, _ := g.s.Quota(context.Background(), g.user)
			want := int64(12)
			if mode == "invalid-usage" {
				want = 0
			}
			if q[0].Used != want || q[0].Reserved != 0 {
				t.Fatal("stream usage not settled correctly")
			}
		})
	}
}
func TestChatRejectsMessagesAndResponsesWithoutUpstreamCall(t *testing.T) {
	for _, protocol := range []string{"messages", "responses"} {
		t.Run(protocol, func(t *testing.T) {
			up := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { t.Error("mismatched request reached upstream") }))
			defer up.Close()
			g := setupRelay(t, up, "new-api", protocol)
			w := chatRequest(g, `{"model":"model","messages":[{"role":"user","content":"hi"}]}`)
			if w.Code != 400 || !strings.Contains(w.Body.String(), "协议不匹配") {
				t.Fatal("protocol mismatch not rejected")
			}
			q, _ := g.s.Quota(context.Background(), g.user)
			if q[0].Used != 0 || q[0].Reserved != 0 {
				t.Fatal("mismatch consumed budget")
			}
		})
	}
}
func TestSingleModelSupportsChatAndResponses(t *testing.T) {
	up := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/v1/chat/completions" {
			io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":"CHAT"}}],"usage":{"prompt_tokens":10,"completion_tokens":2}}`)
		} else if r.URL.Path == "/v1/responses" {
			io.WriteString(w, `{"status":"completed","output":[],"usage":{"input_tokens":10,"output_tokens":2}}`)
		} else {
			t.Error("wrong endpoint")
		}
	}))
	defer up.Close()
	g := setupRelay(t, up, "new-api", "chat")
	ctx := context.Background()
	cfg, _ := g.h.accounts.RuntimeConfig(ctx, g.account)
	cfg.Gateway.Protocols = []string{"chat", "responses"}
	cfg.Gateway.Models = []providers.GatewayModel{{ID: "model", UpstreamID: "upstream-model", Protocols: []string{"chat", "responses"}}}
	if e := g.h.accounts.UpdateRuntime(ctx, g.user, g.account, cfg); e != nil {
		t.Fatal(e)
	}
	if _, e := g.h.accounts.Verify(ctx, g.account); e != nil {
		t.Fatal(e)
	}
	m, _, e := g.s.RoutableModel(ctx, "model")
	if e != nil || !m.SupportsProtocol("chat") || !m.SupportsProtocol("responses") || m.Price == nil {
		t.Fatal("same model lost protocols or price")
	}
	if w := chatRequest(g, `{"model":"model","messages":[{"role":"user","content":"hi"}]}`); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	r := httptest.NewRequest("POST", "/v1/responses", strings.NewReader(`{"model":"model","input":"hi"}`))
	r.Header.Set("Authorization", "Bearer "+g.token)
	w := httptest.NewRecorder()
	g.h.Serve(w, r)
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	q, _ := g.s.Quota(ctx, g.user)
	if q[0].Used != 24 || q[0].Reserved != 0 {
		t.Fatal("shared model price not applied once per request")
	}
}

func TestChatProtocolFailureDoesNotCoolResponses(t *testing.T) {
	up := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/v1/chat/completions" {
			w.WriteHeader(400)
			io.WriteString(w, `{"error":{"code":"model_not_found"}}`)
			return
		}
		io.WriteString(w, `{"status":"completed","output":[],"usage":{"input_tokens":10,"output_tokens":2}}`)
	}))
	defer up.Close()
	g := setupRelay(t, up, "new-api", "chat")
	ctx := context.Background()
	cfg, _ := g.h.accounts.RuntimeConfig(ctx, g.account)
	cfg.Gateway.Protocols = []string{"chat", "responses"}
	cfg.Gateway.Models = []providers.GatewayModel{{ID: "model", UpstreamID: "upstream-model", Protocols: []string{"chat", "responses"}}}
	if e := g.h.accounts.UpdateRuntime(ctx, g.user, g.account, cfg); e != nil {
		t.Fatal(e)
	}
	if _, e := g.h.accounts.Verify(ctx, g.account); e != nil {
		t.Fatal(e)
	}
	if w := chatRequest(g, `{"model":"model","messages":[{"role":"user","content":"hi"}]}`); w.Code != 400 {
		t.Fatal("Chat error not returned")
	}
	r := httptest.NewRequest("POST", "/v1/responses", strings.NewReader(`{"model":"model","input":"hi"}`))
	r.Header.Set("Authorization", "Bearer "+g.token)
	w := httptest.NewRecorder()
	g.h.Serve(w, r)
	if w.Code != 200 {
		t.Fatal("Chat failure blocked Responses", w.Body.String())
	}
	page, e := g.s.AccountModelsPage(ctx, g.account, 1, 20, "")
	if e != nil || len(page.Items) != 1 || !page.Items[0].Routable {
		t.Fatal("model wrongly marked fully unroutable")
	}
	for _, p := range page.Items[0].ProtocolStatus {
		if p.Protocol == "chat" && p.CooldownUntil == 0 || p.Protocol == "responses" && p.CooldownUntil != 0 {
			t.Fatal("protocol cooldown not isolated")
		}
	}
}
