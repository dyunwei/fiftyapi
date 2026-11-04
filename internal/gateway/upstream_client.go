package gateway

import (
	"fiftyapi/internal/accounts"
	"net/http"
)

type upstreamClientContextKey struct{}
type upstreamClientFactory func(accounts.Credential) *http.Client

func (h *Handler) upstreamClient(r *http.Request, c accounts.Credential) *http.Client {
	if factory, ok := r.Context().Value(upstreamClientContextKey{}).(upstreamClientFactory); ok {
		return factory(c)
	}
	return h.client(c.Config)
}
