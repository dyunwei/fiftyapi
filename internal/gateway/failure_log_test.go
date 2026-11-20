package gateway

import (
	"context"
	"errors"
	ws "github.com/coder/websocket"
	"io"
	"strings"
	"testing"
)

func TestFailureCategory(t *testing.T) {
	for _, tc := range []struct {
		err  error
		want string
	}{
		{context.Canceled, "canceled"}, {context.DeadlineExceeded, "deadline_exceeded"},
		{io.EOF, "eof_without_completion"}, {nil, "missing_completion_event"},
		{ws.CloseError{Code: ws.StatusGoingAway, Reason: "secret"}, "websocket_close_1001"},
		{errors.New("secret upstream payload"), "read_or_protocol_error"},
	} {
		if got := failureCategory(tc.err); got != tc.want {
			t.Errorf("got %q want %q", got, tc.want)
		}
	}
	err := websocketFailure("upstream_read", ws.CloseError{Code: ws.StatusGoingAway, Reason: "secret"}, true, safeEventType("secret"))
	got := failureCategory(err)
	if strings.Contains(got, "secret") || !strings.Contains(got, "reused=true") || !strings.Contains(got, "websocket_close_1001") {
		t.Fatal(got)
	}
}

func TestStreamPreservesSafeTransportFailure(t *testing.T) {
	reader, writer := io.Pipe()
	go func() {
		_, _ = io.WriteString(writer, "data: {\"type\":\"response.created\"}\n\n")
		_ = writer.CloseWithError(websocketFailure("upstream_read", ws.CloseError{Code: ws.StatusInternalError, Reason: "private upstream content"}, true, "response.created"))
	}()
	defer reader.Close()
	err := consumeSSE(reader, func(string, []byte) error { return nil })
	got := failureCategory(err)
	want := "stage=upstream_read cause=websocket_close_1011 reused=true last_event=response.created"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}
