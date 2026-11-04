package gateway

import (
	"context"
	"encoding/json"
	"fiftyapi/internal/providers"
	"fiftyapi/internal/store"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func relayServer(g testGateway) *httptest.Server {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/models", g.h.Models)
	mux.HandleFunc("POST /v1/messages", g.h.Serve)
	mux.HandleFunc("POST /v1/responses", g.h.Serve)
	return httptest.NewTLSServer(mux)
}
func TestTwoFiftyAPIGatewaysDiscoveryToolsAndSettlement(t *testing.T) {
	var calls atomic.Int64
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		turn := calls.Add(1)
		var request map[string]json.RawMessage
		json.NewDecoder(r.Body).Decode(&request)
		var model string
		json.Unmarshal(request["model"], &model)
		if model != "upstream-model" {
			t.Error("two-hop model mapping failed")
		}
		w.Header().Set("Content-Type", "application/json")
		if turn == 1 {
			io.WriteString(w, `{"type":"message","role":"assistant","content":[{"type":"tool_use","id":"add-1","name":"add","input":{"a":2,"b":3},"signature":"tool-signature"}],"usage":{"input_tokens":10,"output_tokens":2}}`)
		} else {
			if !strings.Contains(string(request["messages"]), "tool-signature") || !strings.Contains(string(request["messages"]), "tool_result") {
				t.Error("tool history/signature lost through two gateways")
			}
			io.WriteString(w, `{"type":"message","role":"assistant","content":[{"type":"text","text":"5"}],"usage":{"input_tokens":20,"output_tokens":1}}`)
		}
	}))
	defer upstream.Close()
	b := setupRelay(t, upstream, "custom", "messages")
	serverB := relayServer(b)
	defer serverB.Close()
	a := setupRelay(t, serverB, "fiftyapi", "messages")
	cfg, err := a.h.accounts.RuntimeConfig(context.Background(), a.account)
	if err != nil {
		t.Fatal(err)
	}
	cfg.APIKey = b.token
	cfg.Gateway.Discovery = "auto"
	cfg.Gateway.Models = []providers.GatewayModel{{ID: "downstream-model", UpstreamID: "model", Protocol: "messages"}}
	if err = a.h.accounts.UpdateRuntime(context.Background(), a.user, a.account, cfg); err != nil {
		t.Fatal(err)
	}
	names, err := a.h.accounts.Verify(context.Background(), a.account)
	if err != nil || len(names) != 1 || names[0] != "downstream-model" {
		t.Fatalf("real gateway discovery failed: %v", err)
	}
	if err = a.s.SetModel(context.Background(), a.user, "downstream-model", true, &store.Price{Input: 1000000, Output: 1000000}); err != nil {
		t.Fatal(err)
	}
	first := a.request("/v1/messages", `{"model":"downstream-model","max_tokens":32,"messages":[{"role":"user","content":"add 2 and 3"}]}`)
	if first.Code != 200 {
		t.Fatalf("first chain turn failed: %d %s", first.Code, first.Body.String())
	}
	var response struct {
		Content []json.RawMessage `json:"content"`
	}
	if json.Unmarshal(first.Body.Bytes(), &response) != nil {
		t.Fatal("invalid response")
	}
	next, _ := json.Marshal(map[string]any{"model": "downstream-model", "max_tokens": 32, "messages": []any{map[string]any{"role": "user", "content": "add 2 and 3"}, map[string]any{"role": "assistant", "content": response.Content}, map[string]any{"role": "user", "content": []any{map[string]string{"type": "tool_result", "tool_use_id": "add-1", "content": "5"}}}}})
	second := a.request("/v1/messages", string(next))
	if second.Code != 200 || !strings.Contains(second.Body.String(), `"text":"5"`) {
		t.Fatal("tool result continuation failed")
	}
	for _, node := range []testGateway{a, b} {
		windows, err := node.s.Quota(context.Background(), node.user)
		if err != nil {
			t.Fatal(err)
		}
		for _, window := range windows {
			if window.Used != 33 || window.Reserved != 0 {
				t.Fatal("two-hop ledger mismatch")
			}
		}
		logs, err := node.s.Logs(context.Background(), node.user, false, 0, "", 0)
		if err != nil || len(logs) != 2 {
			t.Fatal("two-hop logs missing")
		}
	}
	if calls.Load() != 2 {
		t.Fatal("inference replayed")
	}
	t.Log("two actual fiftyapi gateway handlers: discovery, per-hop model mapping, tool/signature replay and both ledgers/logs passed")
}
func TestTwoGatewayCycleStopsWithoutInferenceOrBilling(t *testing.T) {
	var calls atomic.Int64
	sink := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1); w.WriteHeader(500) }))
	defer sink.Close()
	a, b := setupRelay(t, sink, "fiftyapi", "messages"), setupRelay(t, sink, "fiftyapi", "messages")
	serverA, serverB := relayServer(a), relayServer(b)
	defer serverA.Close()
	defer serverB.Close()
	for _, pair := range []struct {
		node   testGateway
		target *httptest.Server
		key    string
	}{{a, serverB, b.token}, {b, serverA, a.token}} {
		cfg, err := pair.node.h.accounts.RuntimeConfig(context.Background(), pair.node.account)
		if err != nil {
			t.Fatal(err)
		}
		cfg.Gateway.BaseURL = pair.target.URL
		cfg.Gateway.Models = []providers.GatewayModel{{ID: "model", UpstreamID: "model", Protocol: "messages"}}
		cfg.APIKey = pair.key
		if err = pair.node.h.accounts.UpdateRuntime(context.Background(), pair.node.user, pair.node.account, cfg); err != nil {
			t.Fatal(err)
		}
		if _, err = pair.node.h.accounts.Verify(context.Background(), pair.node.account); err != nil {
			t.Fatal(err)
		}
	}
	response := a.request("/v1/messages", `{"model":"model","max_tokens":32,"messages":[{"role":"user","content":"test"}]}`)
	if response.Code != 508 || calls.Load() != 0 {
		t.Fatalf("cycle not stopped: HTTP %d", response.Code)
	}
	for _, node := range []testGateway{a, b} {
		windows, err := node.s.Quota(context.Background(), node.user)
		if err != nil {
			t.Fatal(err)
		}
		for _, window := range windows {
			if window.Used != 0 || window.Reserved != 0 {
				t.Fatal("cycle charged or held quota")
			}
		}
		pending, err := node.s.PendingCharges(context.Background())
		if err != nil || len(pending) != 0 {
			t.Fatal("cycle left pending usage")
		}
	}
}
