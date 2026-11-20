package gateway

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"

	ws "github.com/coder/websocket"
)

// Only locally generated categories are retained: error strings and close reasons
// may contain upstream content, URLs or credentials.
type transportFailure struct{ detail string }

func (e *transportFailure) Error() string { return e.detail }

func failureCategory(err error) string {
	var detail *transportFailure
	if errors.As(err, &detail) {
		return detail.detail
	}
	if errors.Is(err, context.Canceled) {
		return "canceled"
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "deadline_exceeded"
	}
	if code := ws.CloseStatus(err); code != -1 {
		return fmt.Sprintf("websocket_close_%d", code)
	}
	var network net.Error
	if errors.As(err, &network) && network.Timeout() {
		return "network_timeout"
	}
	if errors.Is(err, io.ErrUnexpectedEOF) {
		return "unexpected_eof"
	}
	if errors.Is(err, io.EOF) {
		return "eof_without_completion"
	}
	if err == nil {
		return "missing_completion_event"
	}
	return "read_or_protocol_error"
}

func websocketFailure(stage string, err error, reused bool, last string) error {
	return &transportFailure{detail: fmt.Sprintf("stage=%s cause=%s reused=%t last_event=%s", stage, failureCategory(err), reused, last)}
}

func safeEventType(value string) string {
	switch value {
	case "response.created", "response.in_progress", "response.completed", "response.failed", "response.incomplete", "error", "response.output_text.delta", "response.output_item.added", "response.output_item.done":
		return value
	}
	return "other"
}
