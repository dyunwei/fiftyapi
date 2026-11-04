package gateway

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestIncompleteResponsesUsageAndLateCacheAreSettled(t *testing.T) {
	for _, stream := range []bool{true, false} {
		t.Run(map[bool]string{true: "sse", false: "json"}[stream], func(t *testing.T) {
			up := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if !stream {
					w.Header().Set("Content-Type", "application/json")
					io.WriteString(w, `{"status":"incomplete","usage":{"input_tokens":10,"output_tokens":2,"input_tokens_details":{"cached_tokens":4}}}`)
					return
				}
				w.Header().Set("Content-Type", "text/event-stream")
				io.WriteString(w, "data: {\"type\":\"response.created\",\"response\":{\"usage\":{\"input_tokens\":10,\"output_tokens\":0}}}\n\n")
				io.WriteString(w, "data: {\"type\":\"response.incomplete\",\"response\":{\"usage\":{\"input_tokens\":10,\"output_tokens\":2,\"input_tokens_details\":{\"cached_tokens\":4}}}}\n\n")
			}))
			defer up.Close()
			g := setupGateway(t, "openai", up)
			body, _ := json.Marshal(map[string]any{"model": "model", "input": "test", "stream": stream})
			g.request("/v1/responses", string(body))
			q, err := g.s.Quota(context.Background(), g.user)
			// 6 uncached + 4 cached at 0.1 + 2 output, rounded once = 9.
			if err != nil || q[0].Used != 9 || q[0].Reserved != 0 {
				t.Fatal("incomplete usage/cache normalization wrong", err, q)
			}
			logs, _ := g.s.Logs(context.Background(), g.user, false, 0, "", 0)
			if len(logs) != 1 || logs[0].Input == nil || *logs[0].Input != 6 || logs[0].Amount == nil || !strings.Contains(logs[0].Error, "自动结算") {
				t.Fatal("missing observed usage settlement")
			}
		})
	}
}

func TestInvalidUsageAfterGoodSampleIsNotBilled(t *testing.T) {
	for _, provider := range []string{"claude", "antigravity"} {
		t.Run(provider, func(t *testing.T) {
			up := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				if provider == "claude" {
					io.WriteString(w, "data: {\"type\":\"message_start\",\"message\":{\"usage\":{\"input_tokens\":10,\"output_tokens\":0}}}\n\n")
					io.WriteString(w, "data: {\"type\":\"message_delta\",\"usage\":{\"output_tokens\":-1}}\n\n")
					return
				}
				io.WriteString(w, "data: {\"response\":{\"candidates\":[{\"content\":{\"role\":\"model\",\"parts\":[{\"text\":\"partial\"}]}}],\"usageMetadata\":{\"promptTokenCount\":10,\"candidatesTokenCount\":1}}}\n\n")
				io.WriteString(w, "data: {\"response\":{\"usageMetadata\":{\"promptTokenCount\":10,\"candidatesTokenCount\":2,\"cachedContentTokenCount\":20}}}\n\n")
			}))
			defer up.Close()
			g := setupGateway(t, provider, up)
			g.request("/v1/messages", agRequest)
			q, err := g.s.Quota(context.Background(), g.user)
			if err != nil || q[0].Used != 0 || q[0].Reserved != 0 {
				t.Fatal("invalid usage billed from earlier sample", err, q)
			}
			pending, _ := g.s.PendingCharges(context.Background())
			if len(pending) != 0 {
				t.Fatal("invalid usage left pending")
			}
			logs, _ := g.s.Logs(context.Background(), g.user, false, 0, "", 0)
			if len(logs) != 1 || logs[0].Amount == nil || *logs[0].Amount != 0 || logs[0].Input != nil {
				t.Fatal("invalid sample persisted as reliable usage")
			}
		})
	}
}
