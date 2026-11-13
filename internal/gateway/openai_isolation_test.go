package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	ws "github.com/coder/websocket"
)

func TestOpenAISessionAliases(t *testing.T) {
	base := httptest.NewRequest("POST", "/", nil)
	base.Header.Set("session_id", "conversation")
	expected := sessionHash(base, 1, "model", nil)
	for _, name := range []string{"session-id", "session_id", "x-session-id", "conversation_id", "client_metadata"} {
		r := httptest.NewRequest("POST", "/", nil)
		body := map[string]json.RawMessage{"input": json.RawMessage(`"different text"`)}
		if name == "client_metadata" {
			body[name] = json.RawMessage(`{"session_id":"conversation"}`)
		} else {
			r.Header.Set(name, "conversation")
		}
		if !bytes.Equal(expected, sessionHash(r, 1, "model", body)) {
			t.Fatalf("unrecognized session alias %s", name)
		}
	}
}

func TestOpenAIDeviceSharedAcrossHTTPAndWebsocket(t *testing.T) {
	type observation struct {
		device string
		body   map[string]json.RawMessage
	}
	seen := make(chan observation, 4)
	g, server := websocketGateway(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Upgrade") == "websocket" {
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
				var body map[string]json.RawMessage
				if err = json.Unmarshal(raw, &body); err != nil {
					t.Error(err)
					return
				}
				seen <- observation{r.Header.Get("x-codex-installation-id"), body}
				if err = conn.Write(r.Context(), ws.MessageText, []byte(`{"type":"response.completed","response":{"id":"resp-device","usage":{"input_tokens":1,"output_tokens":1},"output":[]}}`)); err != nil {
					return
				}
			}
		}
		var body map[string]json.RawMessage
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
			return
		}
		seen <- observation{r.Header.Get("x-codex-installation-id"), body}
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, schedulerSuccess)
	})
	device, err := g.s.OpenAIDeviceID(context.Background(), g.account)
	if err != nil {
		t.Fatal(err)
	}
	for _, thread := range []string{"http-thread", "ws-thread"} {
		body := `{"model":"model","input":"hello","client_metadata":{"x-codex-installation-id":"client-device","thread_id":"` + thread + `","session_id":"` + thread + `"}}`
		if thread == "http-thread" {
			if out := g.request("/v1/responses", body); out.Code != 200 {
				t.Fatal(out.Code)
			}
		} else {
			conn := dialGatewayWS(t, g, server)
			sendWS(t, conn, `{"type":"response.create",`+body[1:])
			if event := readWSTerminal(t, conn); string(event["type"]) != `"response.completed"` {
				t.Fatal("websocket device request failed")
			}
		}
		select {
		case out := <-seen:
			var metadata map[string]string
			json.Unmarshal(out.body["client_metadata"], &metadata)
			if out.device != device || metadata["x-codex-installation-id"] != device || metadata["thread_id"] != thread || metadata["session_id"] != thread {
				t.Fatal("transport changed device or conversation identity")
			}
		case <-time.After(5 * time.Second):
			t.Fatal("upstream request missing")
		}
	}
}

func TestOpenAITemporaryLimitKeepsBindingAcrossSchedulerRestart(t *testing.T) {
	for _, stream := range []bool{false, true} {
		t.Run(map[bool]string{false: "http", true: "sse"}[stream], func(t *testing.T) {
			var limited atomic.Bool
			var firstCalls, secondCalls atomic.Int64
			upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Authorization") == "Bearer second-fixture-token" {
					secondCalls.Add(1)
				} else {
					firstCalls.Add(1)
					if limited.Load() {
						w.Header().Set("Retry-After", "120")
						if stream {
							w.Header().Set("Content-Type", "text/event-stream")
							io.WriteString(w, "event: response.failed\ndata: {\"type\":\"response.failed\",\"response\":{\"error\":{\"code\":\"rate_limit_exceeded\"}}}\n\n")
						} else {
							w.WriteHeader(429)
							io.WriteString(w, `{"error":{"code":"rate_limit_exceeded"}}`)
						}
						return
					}
				}
				w.Header().Set("Content-Type", "text/event-stream")
				io.WriteString(w, schedulerSuccess)
			}))
			defer upstream.Close()
			g := setupGateway(t, "openai", upstream)
			second := addPoolAccount(t, g)
			body := `{"model":"model","input":"hello"}`
			if out := poolRequest(g, "stable", body); out.Code != 200 {
				t.Fatal(out.Code)
			}
			limited.Store(true)
			out := poolRequest(g, "stable", body)
			if out.Code != 429 || secondCalls.Load() != 0 || firstCalls.Load() != 2 || out.Header().Get("Retry-After") == "" {
				t.Fatal("temporary limit switched accounts or lost retry hint")
			}
			replacement := New(g.s, g.h.accounts)
			replacement.client = g.h.client
			g.h = replacement
			out = poolRequest(g, "stable", body)
			if out.Code != 429 || firstCalls.Load() != 2 || secondCalls.Load() != 0 {
				t.Fatal("restart bypassed binding hold", out.Code)
			}
			hash := sessionHash(func() *http.Request {
				r := httptest.NewRequest("POST", "/", nil)
				r.Header.Set("session_id", "stable")
				return r
			}(), g.user, "model\x00responses", nil)
			bound, hold, err := g.s.GatewaySessionBinding(context.Background(), hash)
			if err != nil || bound != g.account || hold <= time.Now().Unix() {
				t.Fatal("hold not persisted", err)
			}
			// A concurrent portable claim must not steal a held binding.
			if bound, err = g.s.ClaimGatewaySession(context.Background(), g.user, second, hash, g.account, true); err != nil || bound != g.account {
				t.Fatal("binding hold was overwritten", err)
			}
			if out = poolRequest(g, "fresh", body); out.Code != 200 || secondCalls.Load() != 1 {
				t.Fatal("fresh session cannot use healthy account")
			}
			limited.Store(false)
			if _, err = g.s.DB.Exec("UPDATE gateway_sessions SET hold_until=0"); err != nil {
				t.Fatal(err)
			}
			if _, err = g.s.DB.Exec("DELETE FROM account_cooldowns"); err != nil {
				t.Fatal(err)
			}
			if out = poolRequest(g, "stable", body); out.Code != 200 || firstCalls.Load() != 3 {
				t.Fatal("original session did not recover")
			}
			quota, err := g.s.Quota(context.Background(), g.user)
			if err != nil || quota[0].Reserved != 0 || quota[1].Reserved != 0 {
				t.Fatal("terminal rejection leaked reservations", err)
			}
		})
	}
}
