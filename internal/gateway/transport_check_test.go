package gateway

import (
	"bufio"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestTransportCheckFlushesWithoutUpstreamOrBilling(t *testing.T) {
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("transport check contacted the upstream")
		http.Error(w, "unexpected", 500)
	}))
	defer upstream.Close()
	g := setupGateway(t, "openai", upstream)
	server := httptest.NewServer(http.HandlerFunc(g.h.TransportCheck))
	defer server.Close()

	unauthorized, err := server.Client().Get(server.URL + "/v1/responses?fiftyapi_transport_check=sse")
	if err != nil {
		t.Fatal(err)
	}
	unauthorized.Body.Close()
	if unauthorized.StatusCode != 401 {
		t.Fatalf("unauthenticated transport check returned %d", unauthorized.StatusCode)
	}
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, server.URL+"/v1/responses?fiftyapi_transport_check=sse", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+g.token)
	client := server.Client()
	client.Timeout = 5 * time.Second
	response, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != 200 || response.Header.Get("Content-Type") != "text/event-stream" {
		t.Fatalf("unexpected probe response: %d", response.StatusCode)
	}
	reader := bufio.NewReader(response.Body)
	var previous time.Time
	for _, want := range []string{"1", "2", "3"} {
		event, err := reader.ReadString('\n')
		if err != nil || event != "event: fiftyapi.transport_check\n" {
			t.Fatalf("unexpected SSE event: %q (%v)", event, err)
		}
		data, err := reader.ReadString('\n')
		if err != nil || strings.TrimSpace(data) != "data: "+want {
			t.Fatalf("unexpected SSE data: %q (%v)", data, err)
		}
		separator, err := reader.ReadString('\n')
		if err != nil || separator != "\n" {
			t.Fatalf("SSE frame did not finish: %q (%v)", separator, err)
		}
		now := time.Now()
		if !previous.IsZero() && now.Sub(previous) < 250*time.Millisecond {
			t.Fatalf("events were buffered: gap %v", now.Sub(previous))
		}
		previous = now
	}
	if _, err = io.ReadAll(response.Body); err != nil {
		t.Fatal(err)
	}
	logs, err := g.s.Logs(context.Background(), g.user, false, 0, "", 0)
	if err != nil || len(logs) != 0 {
		t.Fatalf("transport probe created request logs: %v (%v)", logs, err)
	}
	windows, err := g.s.Quota(context.Background(), g.user)
	if err != nil {
		t.Fatal(err)
	}
	for _, window := range windows {
		if window.Used != 0 || window.Reserved != 0 || window.StartedAt != nil {
			t.Fatalf("transport probe opened or charged a budget window: %+v", window)
		}
	}
}
