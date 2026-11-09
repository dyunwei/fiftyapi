package gateway

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// Model a client that disconnects immediately after receiving the terminal
// event, including when the attempt writer flushes an entirely buffered reply.
type completionClientWriter struct {
	*httptest.ResponseRecorder
	cancel               context.CancelFunc
	terminal             string
	partial              bool
	failWrite, failFlush bool
	sawTerminal          bool
}

func (w *completionClientWriter) Write(data []byte) (int, error) {
	if strings.Contains(string(data), w.terminal) {
		w.sawTerminal = true
		if w.cancel != nil {
			w.cancel()
		}
		if w.failWrite {
			return 0, io.ErrClosedPipe
		}
	}
	if w.partial && strings.Contains(string(data), `"delta"`) {
		w.cancel()
	}
	return w.ResponseRecorder.Write(data)
}

func (w *completionClientWriter) FlushError() error {
	if w.sawTerminal && w.failFlush {
		return io.ErrClosedPipe
	}
	w.ResponseRecorder.Flush()
	return nil
}

func TestNativeCompletionAndClientDisconnect(t *testing.T) {
	for _, provider := range []string{"openai", "claude"} {
		for _, scenario := range []string{"completed_disconnect", "buffered_disconnect", "upstream_stays_open", "cancel_before_completion", "terminal_write_failure", "buffered_write_failure", "terminal_flush_failure", "write_failure_without_cancel", "flush_failure_without_cancel"} {
			t.Run(provider+"/"+scenario, func(t *testing.T) {
				partial := scenario == "cancel_before_completion"
				buffered := strings.HasPrefix(scenario, "buffered_")
				terminal := "response.completed"
				initial := "data: {\"type\":\"response.created\",\"response\":{\"usage\":{\"input_tokens\":10,\"output_tokens\":0}}}\n\n"
				content := "data: {\"type\":\"response.output_text.delta\",\"delta\":\"hello\"}\n\n"
				final := "data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_test\",\"status\":\"completed\",\"usage\":{\"input_tokens\":10,\"output_tokens\":2}}}\n\n"
				if provider == "claude" {
					terminal = "message_stop"
					initial = "data: {\"type\":\"message_start\",\"message\":{\"usage\":{\"input_tokens\":10,\"output_tokens\":0}}}\n\n"
					content = "data: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"hello\"}}\n\n"
					final = "data: {\"type\":\"message_delta\",\"usage\":{\"output_tokens\":2}}\n\ndata: {\"type\":\"message_stop\"}\n\n"
				}
				release := make(chan struct{})
				var calls atomic.Int64
				upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					calls.Add(1)
					_, _ = io.Copy(io.Discard, r.Body)
					w.Header().Set("Content-Type", "text/event-stream")
					_, _ = io.WriteString(w, initial)
					if !buffered {
						_, _ = io.WriteString(w, content)
					}
					if !partial {
						_, _ = io.WriteString(w, final)
					}
					w.(http.Flusher).Flush()
					// Keep HTTP open after the terminal event. Protocol completion
					// must end the request without waiting for EOF or a timeout.
					select {
					case <-r.Context().Done():
					case <-release:
					}
				}))
				defer upstream.Close()
				defer close(release)
				g := setupGateway(t, provider, upstream)
				ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
				defer cancel()
				path := "/v1/responses"
				if provider == "claude" {
					path = "/v1/messages"
				}
				request := httptest.NewRequest("POST", path, strings.NewReader(`{"model":"model","stream":true,"input":"hello","messages":[{"role":"user","content":"hello"}]}`)).WithContext(ctx)
				request.Header.Set("Authorization", "Bearer "+g.token)
				writer := &completionClientWriter{ResponseRecorder: httptest.NewRecorder(), terminal: terminal, partial: partial,
					failWrite: scenario == "terminal_write_failure" || scenario == "buffered_write_failure" || scenario == "write_failure_without_cancel",
					failFlush: scenario == "terminal_flush_failure" || scenario == "flush_failure_without_cancel"}
				if scenario != "upstream_stays_open" && !strings.HasSuffix(scenario, "without_cancel") {
					writer.cancel = cancel
				}
				g.h.Serve(writer, request)
				wantStatus := 200
				if partial || writer.failWrite || writer.failFlush {
					wantStatus = 499
				}
				if strings.HasSuffix(scenario, "without_cancel") {
					wantStatus = 502
				}
				if scenario == "upstream_stays_open" && ctx.Err() != nil {
					t.Fatal("waited for EOF after completion")
				}
				logs, err := g.s.Logs(context.Background(), g.user, false, 0, "", 0)
				if err != nil || len(logs) != 1 {
					t.Fatalf("logs: %+v, %v", logs, err)
				}
				entry := logs[0]
				if entry.Status != wantStatus {
					t.Fatalf("status = %d, want %d: %s", entry.Status, wantStatus, entry.Error)
				}
				if wantStatus == 200 && (entry.Error != "" || !writer.sawTerminal) {
					t.Fatalf("successful completion misclassified: %+v", entry)
				}
				if wantStatus == 499 && !strings.Contains(entry.Error, "客户端已取消请求") {
					t.Fatalf("cancellation lost: %s", entry.Error)
				}
				if entry.Amount == nil || entry.Input == nil || *entry.Input != 10 {
					t.Fatalf("usage not settled: %+v", entry)
				}
				if !partial && (entry.Output == nil || *entry.Output != 2 || *entry.Amount != 12) {
					t.Fatalf("terminal usage lost: %+v", entry)
				}
				attempts, err := g.s.RequestAttempts(context.Background(), g.user, true, entry.ID)
				if err != nil || len(attempts) != 1 || attempts[0].Status != wantStatus || attempts[0].Retryable || calls.Load() != 1 {
					t.Fatalf("attempt mismatch or replay: %+v, %v", attempts, err)
				}
				if wantStatus == 200 && attempts[0].Reason != "请求完成" {
					t.Fatalf("wrong routing reason: %+v", attempts[0])
				}
				quota, err := g.s.Quota(context.Background(), g.user)
				if err != nil {
					t.Fatal(err)
				}
				for _, window := range quota {
					if window.Reserved != 0 || window.Used != *entry.Amount {
						t.Fatalf("quota drift: %+v", window)
					}
				}
			})
		}
	}
}
