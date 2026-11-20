package gateway

import (
	"context"
	"fiftyapi/internal/accounts"
	"fiftyapi/internal/providers"
	ws "github.com/coder/websocket"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestUpstreamIdleCloseIsDetectedBeforeNextTurn(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	release := make(chan struct{})
	pinged := make(chan error, 1)
	var connections atomic.Int64
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := connections.Add(1)
		conn, err := ws.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer conn.CloseNow()
		if _, _, err = conn.Read(r.Context()); err != nil {
			return
		}
		_ = conn.Write(r.Context(), ws.MessageText, []byte(`{"type":"response.completed","response":{"id":"resp_idle","usage":{"input_tokens":1,"output_tokens":1}}}`))
		if n == 1 {
			// Ping needs an active reader on each endpoint; the gateway must answer
			// while no inference turn is active.
			readCtx, stop := context.WithCancel(r.Context())
			defer stop()
			go func() { _, _, _ = conn.Read(readCtx) }()
			pingCtx, end := context.WithTimeout(r.Context(), time.Second)
			pinged <- conn.Ping(pingCtx)
			end()
			select {
			case <-release:
			case <-ctx.Done():
				return
			}
			_ = conn.Close(ws.StatusInternalError, "simulated idle failure")
		} else {
			_, _, _ = conn.Read(r.Context())
		}
	}))
	defer server.Close()
	state := &websocketState{connectionID: "test", cache: map[string]wsCachedResponse{}}
	defer state.close()
	makeTransport := func(previous string) *websocketUpstreamTransport {
		return &websocketUpstreamTransport{turn: &websocketTurn{state: state, ctx: ctx, model: "model", previous: previous}, credential: accounts.Credential{AccountID: 1, Config: providers.Config{Provider: "openai"}}, base: server.Client()}
	}
	run := func(previous string) *http.Response {
		req, _ := http.NewRequestWithContext(ctx, "POST", server.URL+"/responses", strings.NewReader(`{"model":"model","input":"hello"}`))
		response, err := makeTransport(previous).RoundTrip(req)
		if err != nil {
			t.Fatal(err)
		}
		_, err = io.ReadAll(response.Body)
		response.Body.Close()
		if err != nil {
			t.Fatal(err)
		}
		return response
	}
	if response := run(""); response.StatusCode != 200 {
		t.Fatal(response.StatusCode)
	}
	if _, ok := state.previous("resp_idle"); !ok {
		t.Fatal("completed context not remembered")
	}
	select {
	case err := <-pinged:
		if err != nil {
			t.Fatal("idle pong missing", err)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	close(release)
	for state.Owner() != 0 {
		select {
		case <-ctx.Done():
			t.Fatal("idle close not detected")
		case <-time.After(time.Millisecond):
		}
	}
	if _, ok := state.previous("resp_idle"); ok {
		t.Fatal("closed connection retained context")
	}
	if response := run("resp_idle"); response.StatusCode != 400 {
		t.Fatal("continuation silently reconstructed", response.StatusCode)
	}
	if connections.Load() != 1 {
		t.Fatal("continuation was dispatched on a new connection")
	}
	if response := run(""); response.StatusCode != 200 {
		t.Fatal(response.StatusCode)
	}
	if connections.Load() != 2 {
		t.Fatal("independent turn did not open a replacement")
	}
}

func TestUpstreamCloseAfterDispatchIsNotReplayed(t *testing.T) {
	var calls atomic.Int64
	g, server := websocketGateway(t, func(w http.ResponseWriter, r *http.Request) {
		conn, err := ws.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer conn.CloseNow()
		if _, _, err = conn.Read(r.Context()); err != nil {
			return
		}
		calls.Add(1)
		_ = conn.Close(ws.StatusInternalError, "simulated execution failure")
	})
	conn := dialGatewayWS(t, g, server)
	sendWS(t, conn, `{"type":"response.create","model":"model","input":"hello"}`)
	event := readWSTerminal(t, conn)
	if string(event["type"]) != `"error"` {
		t.Fatal("expected terminal error", event)
	}
	if calls.Load() != 1 {
		t.Fatal("dispatched request replayed", calls.Load())
	}
}
