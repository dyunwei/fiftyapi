package gateway

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestNativeResponseFormatsAndAccounting(t *testing.T) {
	for _, tc := range []struct {
		name, media, body string
		compact, stream   bool
		status            int
	}{
		{name: "SSE", media: "text/event-stream", status: 200},
		{name: "plain SSE to JSON", media: "text/plain; charset=utf-8", status: 200},
		{name: "plain SSE to stream", media: "text/plain", stream: true, status: 200},
		{name: "binary SSE", media: "application/octet-stream", status: 200},
		{name: "missing type", status: 200},
		{name: "truncated", media: "text/plain", body: "event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\"partial\"}\n\n", status: 502},
		{name: "HTML", media: "text/html", body: "<!doctype html><html>not a model response</html>", status: 502},
		{name: "generic JSON", media: "text/plain", body: `{"status":"completed","usage":{"input_tokens":10,"output_tokens":2,"input_tokens_details":{"cached_tokens":4}}}`, status: 200},
		{name: "explicit JSON is respected", media: "application/json", body: "event: response.completed\ndata: {}\n\n", status: 502},
		{name: "JSON compact", media: "application/json", compact: true, body: `{"object":"response.compaction","usage":{"input_tokens":10,"output_tokens":2,"input_tokens_details":{"cached_tokens":4}}}`, status: 200},
	} {
		t.Run(tc.name, func(t *testing.T) {
			upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				want := "text/event-stream"
				if tc.compact {
					want = "application/json"
					if r.URL.Path != "/inference/compact" {
						t.Error("wrong compact path")
					}
				}
				if r.Header.Get("Accept") != want {
					t.Errorf("Accept = %q, want %q", r.Header.Get("Accept"), want)
				}
				w.Header()["Content-Type"] = nil // Prevent Go's automatic content sniffing.
				if tc.media != "" {
					w.Header().Set("Content-Type", tc.media)
				}
				body := tc.body
				if body == "" {
					body = "event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"id\":\"response-test\",\"status\":\"completed\",\"usage\":{\"input_tokens\":10,\"output_tokens\":2,\"input_tokens_details\":{\"cached_tokens\":4}}}}\n\n"
				}
				io.WriteString(w, body)
			}))
			defer upstream.Close()
			g := setupGateway(t, "openai", upstream)
			path := "/v1/responses"
			if tc.compact {
				path += "/compact"
			}
			stream := "false"
			if tc.stream {
				stream = "true"
			}
			w := g.request(path, `{"model":"model","input":"test","stream":`+stream+`}`)
			if w.Code != tc.status {
				t.Fatalf("HTTP %d, want %d", w.Code, tc.status)
			}
			if tc.status == 200 {
				if tc.stream && !strings.Contains(w.Body.String(), "event: response.completed") {
					t.Fatal("stream not preserved")
				}
				q, e := g.s.Quota(context.Background(), g.user)
				if e != nil || q[0].Used != 9 || q[1].Used != 9 || q[0].Reserved != 0 || q[1].Reserved != 0 {
					t.Fatal("usage was not settled consistently")
				}
			} else {
				q, e := g.s.Quota(context.Background(), g.user)
				if e != nil || q[0].Used != 0 || q[0].Reserved != 0 {
					t.Fatal("invalid response was charged or reservation leaked")
				}
			}
		})
	}
}
