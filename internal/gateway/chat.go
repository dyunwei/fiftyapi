package gateway

import "net/http"

// Chat is a native ingress. Protocol validation and all accounting live in Serve.
// It never rewrites a Chat request into Messages or Responses.
func (h *Handler) ChatCompletions(w http.ResponseWriter, r *http.Request) { h.Serve(w, r) }
