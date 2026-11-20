package gateway

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"fiftyapi/internal/store"
)

func TestRequestDiagnosticsHTTP(t *testing.T) {
	up := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		switch {
		case bytes.Contains(body, []byte("stream-fixture")):
			w.Header().Set("Content-Type", "text/plain")
			io.WriteString(w, "data: {\"type\":\"response.output_text.delta\",\"delta\":\"sample\"}\n\n")
			io.WriteString(w, "data: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\",\"usage\":{\"input_tokens\":10,\"output_tokens\":2}}}\n\n")
		case bytes.Contains(body, []byte("limited-fixture")):
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("Retry-After", "120")
			w.WriteHeader(http.StatusTooManyRequests)
			io.WriteString(w, `{"error":{"message":"private fixture"}}`)
		default:
			w.Header().Set("Content-Type", "application/json")
			io.WriteString(w, `{"status":"completed","usage":{"input_tokens":10,"output_tokens":2}}`)
		}
	}))
	defer up.Close()
	g := setupGateway(t, "openai", up)
	ctx := context.Background()
	check := func(response *httptest.ResponseRecorder) *store.RequestDiagnostics {
		t.Helper()
		page, err := g.s.LogListPage(ctx, store.LogFilter{User: g.user, RequestID: response.Header().Get("X-Request-ID")}, 1, 20)
		if err != nil || len(page.Items) != 1 || page.Items[0].Diagnostics == nil {
			t.Fatalf("missing diagnostic for %d: %+v, %v", response.Code, page, err)
		}
		return page.Items[0].Diagnostics
	}
	if d := check(g.request("/responses", `{"model":"model","input":"json-fixture"}`)); d.Protocol != "responses" || d.ClientTransport != "http_json" || d.UpstreamTransport != "http_json" || !d.Completed {
		t.Fatalf("JSON diagnostics: %+v", d)
	}
	if d := check(g.request("/v1/responses", `{"model":"model","input":"stream-fixture","stream":true}`)); d.ClientTransport != "http_sse" || d.UpstreamTransport != "http_sse" || !d.Completed || !d.OutputStarted {
		t.Fatalf("SSE diagnostics: %+v", d)
	}
	timing := g.h.Performance().FirstOutput
	if timing.Samples != 1 || timing.AverageMS <= 0 {
		t.Fatalf("streaming timing missing or includes JSON: %+v", timing)
	}
	if d := check(g.request("/v1/messages", `{"model":"model"}`)); d.Code != "protocol_mismatch" || d.Dispatched {
		t.Fatalf("protocol diagnostics: %+v", d)
	}
	if d := check(g.request("/v1/responses", `{"model":"model","input":"limited-fixture"}`)); d.Code != "upstream_rate_limit" || d.RetryAt == 0 || d.Completed {
		t.Fatalf("upstream limit diagnostics: %+v", d)
	}
	if err := g.s.SetLimits(ctx, g.user, g.user, 1, 100000000); err != nil {
		t.Fatal(err)
	}
	if d := check(g.request("/v1/responses", `{"model":"model"}`)); d.Code != "local_quota" || d.Dispatched || len(d.Quota) != 1 || d.Quota[0].Window != "5h" || d.Quota[0].ResetAt == nil {
		t.Fatalf("local quota diagnostics: %+v", d)
	}
}

func TestAuthenticatedWebSocketHandshakeFailureHasDiagnostic(t *testing.T) {
	up := httptest.NewTLSServer(http.NotFoundHandler())
	defer up.Close()
	g := setupGateway(t, "openai", up)
	request := httptest.NewRequest(http.MethodGet, "/v1/responses", nil)
	request.Header.Set("Authorization", "Bearer "+g.token)
	response := httptest.NewRecorder()
	g.h.ServeWebSocket(response, request)
	result, err := g.s.LogListPage(context.Background(), store.LogFilter{User: g.user, RequestID: response.Header().Get("X-Request-ID")}, 1, 20)
	if response.Code < 400 || response.Header().Get("X-Request-ID") == "" || err != nil || len(result.Items) != 1 {
		t.Fatalf("missing handshake failure log: %d, %v, %v", response.Code, result, err)
	}
	if d := result.Items[0].Diagnostics; d == nil || d.Code != "client_websocket_handshake" || d.ClientTransport == "websocket" || d.Dispatched {
		t.Fatalf("incorrect handshake diagnostics: %+v", d)
	}
}
