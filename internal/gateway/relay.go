package gateway

import (
	"bytes"
	"encoding/json"
	"errors"
	"fiftyapi/internal/accounts"
	"fiftyapi/internal/buildinfo"
	"fiftyapi/internal/providers"
	"fiftyapi/internal/store"
	"net/http"
	"net/url"
	"strings"
)

func sameOriginHost(target, host, scheme string) bool {
	u, err := url.Parse(target)
	if err != nil {
		return false
	}
	incoming, err := url.Parse(scheme + "://" + host)
	if err != nil {
		return false
	}
	port := func(u *url.URL) string {
		if p := u.Port(); p != "" {
			return p
		}
		if u.Scheme == "https" {
			return "443"
		}
		return "80"
	}
	return strings.EqualFold(u.Hostname(), incoming.Hostname()) && port(u) == port(incoming)
}
func (h *Handler) relay(w http.ResponseWriter, r *http.Request, request map[string]json.RawMessage, m store.Model, c accounts.Credential, output int64) result {
	out := result{status: 502}
	g := c.Config.Gateway
	protocol := m.Protocol
	if protocol == "" {
		protocol = providers.ModelProtocol(m.Provider)
	}
	if g == nil || !g.Supports(protocol) {
		out.status = 400
		out.err = errors.New("网关账号未启用所需协议")
		return out
	}
	endpoint := protocol
	if protocol == "chat" {
		endpoint = "chat/completions"
	}
	if strings.HasSuffix(r.URL.Path, "/compact") {
		if protocol != "responses" || !g.Compact {
			out.status = 400
			out.err = errors.New("此网关未启用 Responses compact")
			return out
		}
		endpoint += "/compact"
	}
	target, err := providers.GatewayEndpoint(*g, endpoint)
	if err != nil {
		out.err = err
		return out
	}
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	if sameOriginHost(target, r.Host, scheme) {
		out.status = 508
		out.err = errors.New("上游地址指向当前网关，已阻止循环转发")
		return out
	}
	chain, err := providers.NextRelayChain(r.Header.Get(providers.RelayChainHeader), h.instanceID)
	if err != nil {
		out.status = 508
		out.err = err
		return out
	}
	// Preserve the public protocol and tool/signature fields. OAuth-only system,
	// identity, store/stream and sampling normalization must never run here.
	request["model"], _ = json.Marshal(m.UpstreamID)
	if protocol == "messages" && len(request["max_tokens"]) == 0 {
		request["max_tokens"], _ = json.Marshal(output)
	}
	var requestedStream bool
	_ = json.Unmarshal(request["stream"], &requestedStream)
	if protocol == "chat" && requestedStream {
		options := map[string]json.RawMessage{}
		if raw := request["stream_options"]; len(raw) > 0 && string(raw) != "null" {
			if json.Unmarshal(raw, &options) != nil || options == nil {
				out.status = 400
				out.err = errors.New("stream_options 必须为对象")
				return out
			}
		}
		options["include_usage"] = json.RawMessage("true")
		request["stream_options"], _ = json.Marshal(options)
	}
	body, err := json.Marshal(request)
	if err != nil {
		out.status = 400
		out.err = errors.New("请求 JSON 无效")
		return out
	}
	req, err := http.NewRequestWithContext(r.Context(), "POST", target, bytes.NewReader(body))
	if err != nil {
		out.err = errors.New("无法构造网关请求")
		return out
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	ua := c.Config.UserAgent
	if ua == "" {
		ua = "FiftyAPI/" + buildinfo.Version()
	}
	req.Header.Set("User-Agent", ua)
	for _, name := range []string{"anthropic-version", "anthropic-beta", "openai-beta", "session-id", "session_id", "conversation_id", "x-session-id", "x-codex-turn-metadata"} {
		if value := r.Header.Get(name); value != "" {
			req.Header.Set(name, value)
		}
	}
	if protocol == "messages" && req.Header.Get("anthropic-version") == "" {
		req.Header.Set("anthropic-version", "2023-06-01")
	}
	for k, v := range c.Config.GatewayHeaders {
		req.Header.Set(k, v)
	}
	if c.Config.InferencePolicy.ForceStandard {
		req.Header.Del("OpenAI-Service-Tier")
	}
	providers.GatewayAuth(req.Header, c.Config, c.APIKey)
	req.Header.Set(providers.RelayChainHeader, chain)
	client := h.upstreamClient(r, c)
	defer client.CloseIdleConnections()
	out.dispatched = true
	resp, err := client.Do(req)
	if err != nil {
		out.err = errors.New("上游网关连接失败")
		return out
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		out.status = resp.StatusCode
		if out.status < 400 {
			out.status = 502
		}
		out.err = errors.New("上游拒绝请求，请检查模型权限或账号状态")
		if out.status == 400 || out.status == 401 || out.status == 403 || out.status == 404 || out.status == 429 || out.status == 508 {
			out.known = true
			out.complete = true
		}
		classifyRejection(&out, resp)
		return out
	}
	observeHeaders(&out, resp.Header)
	var stream bool
	json.Unmarshal(request["stream"], &stream)
	provider := "claude"
	if protocol == "responses" {
		provider = "openai"
	} else if protocol == "chat" {
		return consumeChatResponse(w, r, resp, stream, out)
	}
	return consumeProtocolResponse(w, r, resp, provider, stream, out)
}

// Explicit HTTP is supported for trusted private deployments. It is never
// enabled implicitly by loopback/DNS detection or a redirect from HTTPS.
