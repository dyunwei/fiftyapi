package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestProbePinsModelAndValidatesProtocol(t *testing.T) {
	calls := 0
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		if body["model"] != "upstream-model" || r.URL.Path != "/v1/chat/completions" {
			t.Error("incorrect model or protocol", body, r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer upstream-private-test-key" {
			t.Error("missing upstream auth")
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"Hello"}}],"usage":{"prompt_tokens":1,"completion_tokens":1}}`))
	}))
	defer upstream.Close()
	g := setupRelay(t, upstream, "custom", "chat")
	for _, tc := range []struct{ model, protocol string }{{"missing", "chat"}, {"model", "responses"}} {
		if _, err := g.h.Probe(context.Background(), g.account, tc.model, tc.protocol, "localhost"); err == nil {
			t.Error("invalid probe accepted")
		}
	}
	if calls != 0 {
		t.Fatal("invalid probe contacted upstream")
	}
	result, err := g.h.Probe(context.Background(), g.account, "model", "chat", "localhost")
	if err != nil || result.Error != "" || result.Status != 200 || !strings.Contains(result.Response, "Hello") {
		t.Fatalf("probe failed: %+v %v", result, err)
	}
	if calls != 1 {
		t.Fatalf("calls=%d", calls)
	}
	if strings.Contains(string(result.Request)+result.Response, "upstream-private-test-key") {
		t.Fatal("credential leaked")
	}
}

func TestProbeWriterBoundsResponse(t *testing.T) {
	w := &probeWriter{}
	body := []byte(strings.Repeat("x", 300<<10))
	n, err := w.Write(body)
	if err != nil || n != len(body) || w.body.Len() != 256<<10 || !w.truncated {
		t.Fatal("response limit failed")
	}
}
