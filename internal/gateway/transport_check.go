package gateway

import (
	"net/http"
	"strconv"
	"time"
)

// TransportCheck sends only fixed local events and leaves account routing and
// billing untouched. The clock starts again after each flush so a slow writer
// cannot cause queued timer ticks to emit events together.
func (h *Handler) TransportCheck(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet || r.URL.Query().Get("fiftyapi_transport_check") != "sse" {
		w.Header().Set("Allow", "POST")
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	if _, _, err := h.auth(r); err != nil {
		failure(w, http.StatusUnauthorized, "API Key 无效")
		return
	}
	if h.sseChecks.Add(1) > 16 {
		h.sseChecks.Add(-1)
		failure(w, http.StatusTooManyRequests, "传输探测正在进行，请稍后再试")
		return
	}
	defer h.sseChecks.Add(-1)
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Accel-Buffering", "no")
	defer http.NewResponseController(w).SetWriteDeadline(time.Time{})
	for seq := 1; seq <= 3; seq++ {
		if seq > 1 {
			timer := time.NewTimer(600 * time.Millisecond)
			select {
			case <-timer.C:
			case <-r.Context().Done():
				timer.Stop()
				return
			}
		}
		if err := emitSSE(w, "fiftyapi.transport_check", []byte(strconv.Itoa(seq))); err != nil {
			return
		}
	}
}
