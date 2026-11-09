package gateway

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"time"
)

// Delay only protocol setup events. Actual text, reasoning, function/tool
// output, unknown events, or a bounded buffer limit commit the response and
// permanently forbid a replay. Headers and partial failed-attempt IDs stay local.
type attemptWriter struct {
	parent      http.ResponseWriter
	header      http.Header
	status      int
	pending     bytes.Buffer
	committed   bool
	firstOutput *time.Time
	scanAt      int
	writeErr    error
	heartbeatAt time.Time
}

func newAttemptWriter(w http.ResponseWriter) *attemptWriter {
	return &attemptWriter{parent: w, header: w.Header().Clone(), heartbeatAt: time.Now()}
}
func (w *attemptWriter) Header() http.Header         { return w.header }
func (w *attemptWriter) Unwrap() http.ResponseWriter { return w.parent }
func (w *attemptWriter) WriteHeader(code int) {
	if w.status == 0 {
		w.status = code
	}
}
func (w *attemptWriter) commit() error {
	if w.committed {
		return nil
	}
	w.committed = true
	for k := range w.parent.Header() {
		w.parent.Header().Del(k)
	}
	for k, v := range w.header {
		w.parent.Header()[k] = append([]string(nil), v...)
	}
	status := w.status
	if status == 0 {
		status = 200
	}
	w.parent.WriteHeader(status)
	if w.pending.Len() > 0 {
		_, err := io.Copy(w.parent, &w.pending)
		return err
	}
	return nil
}
func (w *attemptWriter) Write(b []byte) (int, error) {
	if w.committed {
		return w.parent.Write(b)
	}
	if w.status == 0 {
		w.status = 200
	}
	if w.header.Get("Content-Type") != "text/event-stream" {
		if err := w.commit(); err != nil {
			return 0, err
		}
		return w.parent.Write(b)
	}
	w.pending.Write(b)
	data := w.pending.Bytes()
	semantic := false
	for w.scanAt < len(data) {
		at := bytes.Index(data[w.scanAt:], []byte("\n\n"))
		if at < 0 {
			break
		}
		end := w.scanAt + at + 2
		frame := data[w.scanAt:end]
		w.scanAt = end
		if semanticFrame(frame) {
			semantic = true
			break
		}
	}
	if semantic || w.pending.Len() > 256<<10 {
		if semantic {
			now := time.Now()
			w.firstOutput = &now
		}
		if err := w.commit(); err != nil {
			return 0, err
		}
	}
	return len(b), nil
}
func (w *attemptWriter) Flush() {
	_ = w.FlushError()
}
func (w *attemptWriter) FlushError() error {
	if w.writeErr != nil {
		return w.writeErr
	}
	if w.committed {
		w.writeErr = flushResponse(w.parent)
		return w.writeErr
	}
	if time.Since(w.heartbeatAt) >= 15*time.Second {
		for k, v := range w.header {
			w.parent.Header()[k] = append([]string(nil), v...)
		}
		_, w.writeErr = io.WriteString(w.parent, ": keepalive\n\n")
		if w.writeErr == nil {
			w.writeErr = flushResponse(w.parent)
		}
		w.heartbeatAt = time.Now()
	}
	return w.writeErr
}

func semanticFrame(frame []byte) bool {
	for _, line := range bytes.Split(frame, []byte("\n")) {
		if !bytes.HasPrefix(line, []byte("data:")) {
			continue
		}
		raw := bytes.TrimSpace(line[5:])
		if bytes.Equal(raw, []byte("[DONE]")) {
			return false
		}
		var data struct {
			Type         string `json:"type"`
			ContentBlock struct {
				Type     string `json:"type"`
				Text     string `json:"text"`
				Thinking string `json:"thinking"`
			} `json:"content_block"`
			Part struct {
				Text    string `json:"text"`
				Refusal string `json:"refusal"`
			} `json:"part"`
			Item struct {
				Type string `json:"type"`
			} `json:"item"`
		}
		if json.Unmarshal(raw, &data) != nil {
			return true
		}
		switch data.Type {
		case "response.created", "response.queued", "response.in_progress", "response.completed", "response.failed", "response.incomplete", "error", "message_start", "message_delta", "message_stop", "ping":
			continue
		case "response.content_part.added", "response.content_part.done":
			if data.Part.Text != "" || data.Part.Refusal != "" {
				return true
			}
			continue
		case "content_block_start":
			if (data.ContentBlock.Type != "" && data.ContentBlock.Type != "text" && data.ContentBlock.Type != "thinking") || data.ContentBlock.Text != "" || data.ContentBlock.Thinking != "" {
				return true
			}
			continue
		case "response.output_item.added":
			if data.Item.Type != "" && data.Item.Type != "message" && data.Item.Type != "reasoning" {
				return true
			}
			continue
		case "response.output_item.done", "response.output_text.done":
			return true
		default:
			return true
		}
	}
	return false
}
