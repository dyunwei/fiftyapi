package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	ws "github.com/coder/websocket"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func websocketGateway(t *testing.T, handler http.HandlerFunc) (testGateway, *httptest.Server) {
	t.Helper()
	upstream := httptest.NewTLSServer(handler)
	t.Cleanup(upstream.Close)
	g := setupGateway(t, "openai", upstream)
	cfg, e := g.h.accounts.RuntimeConfig(context.Background(), g.account)
	if e != nil {
		t.Fatal(e)
	}
	cfg.GatewayURL = upstream.URL + "/responses"
	if e = g.h.accounts.UpdateRuntime(context.Background(), g.user, g.account, cfg); e != nil {
		t.Fatal(e)
	}
	if e = g.s.AccountState(context.Background(), g.account, "catalog_ready", ""); e != nil {
		t.Fatal(e)
	}
	server := httptest.NewServer(http.HandlerFunc(g.h.ServeWebSocket))
	t.Cleanup(server.Close)
	return g, server
}
func dialGatewayWS(t *testing.T, g testGateway, server *httptest.Server) *ws.Conn {
	t.Helper()
	ctx, stop := context.WithTimeout(context.Background(), 5*time.Second)
	defer stop()
	headers := http.Header{"Authorization": []string{"Bearer " + g.token}}
	conn, response, e := ws.Dial(ctx, "ws"+strings.TrimPrefix(server.URL, "http"), &ws.DialOptions{HTTPHeader: headers})
	if e != nil {
		status := 0
		if response != nil {
			status = response.StatusCode
		}
		t.Fatalf("WS dial failed: %d", status)
	}
	t.Cleanup(func() { conn.CloseNow() })
	return conn
}
func sendWS(t *testing.T, conn *ws.Conn, body string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if e := conn.Write(ctx, ws.MessageText, []byte(body)); e != nil {
		t.Fatal("WS write failed")
	}
}
func readWSTerminal(t *testing.T, conn *ws.Conn) map[string]json.RawMessage {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for {
		_, raw, e := conn.Read(ctx)
		if e != nil {
			t.Fatal("WS closed before terminal event")
		}
		var event map[string]json.RawMessage
		if json.Unmarshal(raw, &event) != nil {
			t.Fatal("WS event invalid")
		}
		var kind string
		json.Unmarshal(event["type"], &kind)
		if kind == "response.completed" || kind == "error" || kind == "response.failed" {
			return event
		}
	}
}
func TestNativeWebsocketContinuationIsolationAndAccounting(t *testing.T) {
	var connects, calls atomic.Int64
	var mu sync.Mutex
	seenPrevious := []string{}
	g, server := websocketGateway(t, func(w http.ResponseWriter, r *http.Request) {
		connects.Add(1)
		if r.URL.Path != "/responses" || !strings.Contains(r.Header.Get("OpenAI-Beta"), "responses_websockets=2026-02-06") {
			t.Error("wrong upstream websocket handshake")
		}
		conn, e := ws.Accept(w, r, nil)
		if e != nil {
			return
		}
		defer conn.CloseNow()
		for {
			_, raw, e := conn.Read(r.Context())
			if e != nil {
				return
			}
			var payload struct {
				Type     string `json:"type"`
				Previous string `json:"previous_response_id"`
				Stream   string `json:"stream_id"`
				Store    bool   `json:"store"`
				Model    string `json:"model"`
			}
			if json.Unmarshal(raw, &payload) != nil || payload.Type != "response.create" || payload.Store || payload.Model != "model" {
				t.Error("invalid native websocket payload")
				return
			}
			mu.Lock()
			seenPrevious = append(seenPrevious, payload.Previous)
			mu.Unlock()
			n := calls.Add(1)
			event := fmt.Sprintf(`{"type":"response.completed","response":{"id":"resp_%d","status":"completed","usage":{"input_tokens":10,"output_tokens":2,"input_tokens_details":{"cached_tokens":4}},"output":[{"type":"message","content":[{"type":"output_text","text":"OK"}]}]}}`, n)
			if e = conn.Write(r.Context(), ws.MessageText, []byte(event)); e != nil {
				return
			}
		}
	})
	conn := dialGatewayWS(t, g, server)
	sendWS(t, conn, `{"type":"response.create","model":"model","stream_id":"main","input":"hello"}`)
	event := readWSTerminal(t, conn)
	if string(event["type"]) != `"response.completed"` || string(event["stream_id"]) != `"main"` {
		t.Fatal("first WS response failed")
	}
	sendWS(t, conn, `{"type":"response.create","previous_response_id":"resp_1","stream_id":"main","input":"next"}`)
	event = readWSTerminal(t, conn)
	if string(event["type"]) != `"response.completed"` {
		t.Fatal("continuation failed")
	}
	if connects.Load() != 1 || calls.Load() != 2 {
		t.Fatal("native connection was not reused")
	}
	mu.Lock()
	previous := seenPrevious[1]
	mu.Unlock()
	if previous != "resp_1" {
		t.Fatal("previous response lineage was lost")
	}
	// A separate socket cannot borrow the first socket's response state.
	other := dialGatewayWS(t, g, server)
	sendWS(t, other, `{"type":"response.create","model":"model","previous_response_id":"resp_1","input":"borrow"}`)
	denied := readWSTerminal(t, other)
	if !strings.Contains(string(denied["error"]), "previous_response_not_found") || calls.Load() != 2 {
		t.Fatal("response cache leaked across sockets")
	}
	deadline := time.Now().Add(3 * time.Second)
	for {
		q, e := g.s.Quota(context.Background(), g.user)
		if e == nil && q[0].Reserved == 0 && q[0].Used == 18 && q[1].Used == 18 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("websocket turns did not settle exactly once")
		}
		time.Sleep(5 * time.Millisecond)
	}
	logs, e := g.s.Logs(context.Background(), g.user, false, 0, "", 0)
	if e != nil || len(logs) != 2 {
		t.Fatal("websocket logical request logs missing")
	}
	for _, entry := range logs {
		attempts, e := g.s.RequestAttempts(context.Background(), g.user, true, entry.ID)
		if e != nil || len(attempts) != 1 {
			t.Fatal("websocket attempt history missing")
		}
	}
}
func TestWebsocketDisconnectSettlesObservedUsage(t *testing.T) {
	stopped := make(chan struct{})
	g, server := websocketGateway(t, func(w http.ResponseWriter, r *http.Request) {
		conn, e := ws.Accept(w, r, nil)
		if e != nil {
			return
		}
		defer conn.CloseNow()
		defer close(stopped)
		if _, _, e = conn.Read(r.Context()); e != nil {
			return
		}
		for _, event := range []string{`{"type":"response.created","response":{"id":"resp_cancel","usage":{"input_tokens":10,"output_tokens":0}}}`, `{"type":"response.output_text.delta","delta":"PARTIAL"}`} {
			if e = conn.Write(r.Context(), ws.MessageText, []byte(event)); e != nil {
				return
			}
		}
		_, _, _ = conn.Read(r.Context())
	})
	conn := dialGatewayWS(t, g, server)
	sendWS(t, conn, `{"type":"response.create","model":"model","input":"hello"}`)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	for {
		_, raw, e := conn.Read(ctx)
		if e != nil {
			t.Fatal("no partial output")
		}
		if strings.Contains(string(raw), "PARTIAL") {
			break
		}
	}
	conn.CloseNow()
	select {
	case <-stopped:
	case <-time.After(3 * time.Second):
		t.Fatal("disconnect did not close upstream")
	}
	deadline := time.Now().Add(3 * time.Second)
	for {
		q, e := g.s.Quota(context.Background(), g.user)
		if e == nil && q[0].Reserved == 0 && q[0].Used == 10 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("cancel lost observed usage or reservation")
		}
		time.Sleep(5 * time.Millisecond)
	}
	active, waiting := g.h.AccountLoad(g.account)
	if active != 0 || waiting != 0 {
		t.Fatal("disconnect leaked scheduling resources")
	}
}
func TestWebsocketAuthenticationOriginAndRevocation(t *testing.T) {
	var calls atomic.Int64
	g, server := websocketGateway(t, func(w http.ResponseWriter, r *http.Request) {
		conn, e := ws.Accept(w, r, nil)
		if e != nil {
			return
		}
		defer conn.CloseNow()
		for {
			_, _, e = conn.Read(r.Context())
			if e != nil {
				return
			}
			calls.Add(1)
			_ = conn.Write(r.Context(), ws.MessageText, []byte(`{"type":"response.completed","response":{"id":"resp_auth","status":"completed","usage":{"input_tokens":1,"output_tokens":1}}}`))
		}
	})
	for _, headers := range []http.Header{{}, {"Authorization": []string{"Bearer " + g.token}, "Origin": []string{"https://foreign.example"}}} {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		conn, resp, e := ws.Dial(ctx, "ws"+strings.TrimPrefix(server.URL, "http"), &ws.DialOptions{HTTPHeader: headers})
		cancel()
		if e == nil {
			conn.CloseNow()
			t.Fatal("unauthorized or cross-origin connection accepted")
		}
		if resp == nil || (resp.StatusCode != 401 && resp.StatusCode != 403) {
			t.Fatal("wrong upgrade rejection")
		}
	}
	conn := dialGatewayWS(t, g, server)
	if _, e := g.s.DB.Exec("UPDATE api_keys SET enabled=0"); e != nil {
		t.Fatal(e)
	}
	sendWS(t, conn, `{"type":"response.create","model":"model","input":"hello"}`)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_, _, _ = conn.Read(ctx)
	if calls.Load() != 0 {
		t.Fatal("revoked key sent a new upstream request")
	}
}

func TestWebsocketShutdownDrainsBeforeDatabaseClose(t *testing.T) {
	g, server := websocketGateway(t, func(w http.ResponseWriter, r *http.Request) {
		conn, e := ws.Accept(w, r, nil)
		if e != nil {
			return
		}
		defer conn.CloseNow()
		if _, _, e = conn.Read(r.Context()); e != nil {
			return
		}
		for _, event := range []string{`{"type":"response.created","response":{"id":"resp_stop","usage":{"input_tokens":10,"output_tokens":0}}}`, `{"type":"response.output_text.delta","delta":"STARTED"}`} {
			if conn.Write(r.Context(), ws.MessageText, []byte(event)) != nil {
				return
			}
		}
		_, _, _ = conn.Read(r.Context())
	})
	conn := dialGatewayWS(t, g, server)
	sendWS(t, conn, `{"type":"response.create","model":"model","input":"hello"}`)
	ctx, stop := context.WithTimeout(context.Background(), 5*time.Second)
	defer stop()
	for {
		_, raw, e := conn.Read(ctx)
		if e != nil {
			t.Fatal("no output before shutdown")
		}
		if strings.Contains(string(raw), "STARTED") {
			break
		}
	}
	if e := g.h.ShutdownWebSockets(ctx); e != nil {
		t.Fatal(e)
	}
	q, e := g.s.Quota(ctx, g.user)
	if e != nil || q[0].Reserved != 0 || q[0].Used != 10 {
		t.Fatal("shutdown returned before observed usage was settled")
	}
	_, response, e := ws.Dial(ctx, "ws"+strings.TrimPrefix(server.URL, "http"), &ws.DialOptions{HTTPHeader: http.Header{"Authorization": []string{"Bearer " + g.token}}})
	if e == nil || response == nil || response.StatusCode != 503 {
		t.Fatal("new socket admitted during shutdown")
	}
}
