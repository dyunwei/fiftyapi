package gateway

import (
	"crypto/rand"
	"encoding/base64"
	"mime"
	"net/http"
	"strings"
)

func newRequestID() (string, error) {
	var nonce [24]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(nonce[:]), nil
}

func gatewayProtocol(path string) string {
	switch path {
	case "/v1/responses", "/v1/responses/compact", "/responses", "/responses/compact":
		return "responses"
	case "/v1/chat/completions", "/chat/completions":
		return "chat"
	case "/v1internal:streamGenerateContent", "/v1internal:generateContent":
		return "antigravity"
	default:
		return "messages"
	}
}

func httpTransport(contentType string) string {
	media, _, _ := mime.ParseMediaType(contentType)
	switch media {
	case "text/event-stream":
		return "http_sse"
	case "application/json":
		return "http_json"
	default:
		if strings.HasSuffix(media, "+json") {
			return "http_json"
		}
		return "http"
	}
}

func diagnosticCode(out result, wsHandshakeFailed bool) string {
	if out.status == 499 {
		return "client_cancelled"
	}
	if wsHandshakeFailed {
		return "upstream_websocket_handshake"
	}
	if out.status == 429 || out.signal == "rate_limit" || out.signal == "quota_exhausted" {
		return "upstream_rate_limit"
	}
	if out.err == nil {
		return ""
	}
	if !out.dispatched {
		return "upstream_unavailable"
	}
	if !out.complete && (out.upstreamTransport == "http_sse" || out.upstreamTransport == "websocket") {
		return "stream_interrupted"
	}
	return "upstream_error"
}

func (out *result) observeHTTPResponse(resp *http.Response) {
	out.upstreamTransport = httpTransport(resp.Header.Get("Content-Type"))
}

func (out *result) eventStream(resp *http.Response) bool {
	if nativeEventStream(resp) {
		out.upstreamTransport = "http_sse"
		return true
	}
	return false
}
