package gateway

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fiftyapi/internal/accounts"
	"fiftyapi/internal/providers"
	"fiftyapi/internal/store"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strings"
)

func (h *Handler) forward(w http.ResponseWriter, r *http.Request, body []byte, request map[string]json.RawMessage, m store.Model, c accounts.Credential, output int64) result {
	if err := applyInferencePolicy(request, m.Protocol, c.Config.InferencePolicy, c.Config.Provider == "antigravity"); err != nil {
		return result{status: 400, known: true, complete: true, err: err}
	}
	if c.Config.Provider == "gateway" {
		return h.relay(w, r, request, m, c, output)
	}
	if m.Provider == "antigravity" {
		body, _ = json.Marshal(request)
		return h.antigravity(w, r, body, m, c, output)
	}
	out := result{status: 502}
	var stream bool
	json.Unmarshal(request["stream"], &stream)
	request["model"], _ = json.Marshal(m.UpstreamID)
	endpoint := c.Config.GatewayURL
	compact := strings.HasSuffix(r.URL.Path, "/compact")
	if m.Provider == "claude" {
		if err := normalizeClaude(request, r, c); err != nil {
			out.status = 400
			out.err = err
			return out
		}
		if endpoint == "" {
			endpoint = "https://api.anthropic.com/v1/messages"
		}
		if len(request["max_tokens"]) == 0 {
			request["max_tokens"], _ = json.Marshal(output)
		}
	} else {
		if endpoint == "" {
			endpoint = "https://chatgpt.com/backend-api/codex/responses"
		}
		if compact {
			endpoint = strings.TrimSuffix(endpoint, "/") + "/compact"
		}
		request["store"] = json.RawMessage("false")
		request["stream"] = json.RawMessage("true")
		if compact {
			request["stream"] = json.RawMessage("false")
		}
		for _, key := range []string{"max_output_tokens", "max_completion_tokens", "temperature", "top_p", "frequency_penalty", "presence_penalty", "chat_template_kwargs", "user", "metadata", "prompt_cache_retention", "safety_identifier", "stream_options", "truncation", "stop_sequences"} {
			delete(request, key)
		}
		if len(request["instructions"]) == 0 {
			request["instructions"], _ = json.Marshal("You are a coding assistant.")
		}
		var includes []string
		if raw := request["include"]; len(raw) > 0 {
			if json.Unmarshal(raw, &includes) != nil {
				out.status = 400
				out.err = errors.New("include 字段格式无效")
				return out
			}
		}
		hasReasoning := false
		for _, value := range includes {
			if value == "reasoning.encrypted_content" {
				hasReasoning = true
			}
		}
		if !hasReasoning {
			includes = append(includes, "reasoning.encrypted_content")
		}
		request["include"], _ = json.Marshal(includes)
		var input string
		if json.Unmarshal(request["input"], &input) == nil {
			request["input"], _ = json.Marshal([]any{map[string]any{"type": "message", "role": "user", "content": input}})
		}
	}
	u, err := url.Parse(endpoint)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil {
		out.err = errors.New("上游网关地址无效")
		return out
	}
	encoded, _ := json.Marshal(request)
	req, err := http.NewRequestWithContext(r.Context(), http.MethodPost, u.String(), bytes.NewReader(encoded))
	if err != nil {
		out.err = errors.New("无法构造上游请求")
		return out
	}
	req.Header.Set("Content-Type", "application/json")
	// Native Codex always streams upstream, even when the client requests JSON.
	// compact has a separate JSON response contract.
	if m.Provider == "openai" && !compact {
		req.Header.Set("Accept", "text/event-stream")
	} else {
		req.Header.Set("Accept", "application/json, text/event-stream")
		if compact {
			req.Header.Set("Accept", "application/json")
		}
	}
	req.Header.Set("Authorization", "Bearer "+c.Token.AccessToken)
	providers.IdentityHeaders(req.Header, c.Config)
	if m.Provider == "openai" {
		providers.SetOpenAIAccountHeader(req.Header, c.Config, c.Token)
	}
	for _, name := range []string{"session_id", "conversation_id", "x-codex-turn-metadata", "openai-beta"} {
		if value := r.Header.Get(name); value != "" {
			req.Header.Set(name, value)
		}
	}
	if m.Provider == "claude" {
		if beta := r.Header.Get("anthropic-beta"); beta != "" {
			req.Header.Set("anthropic-beta", beta+",oauth-2025-04-20")
		}
	}
	for k, v := range c.Config.GatewayHeaders {
		req.Header.Set(k, v)
	}
	if c.Config.InferencePolicy.ForceStandard {
		req.Header.Del("OpenAI-Service-Tier")
	}
	client := h.upstreamClient(r, c)
	defer client.CloseIdleConnections()
	out.dispatched = true
	resp, err := client.Do(req)
	if err != nil {
		out.err = errors.New("上游连接失败，无法确认用量")
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
	return consumeProtocolResponse(w, r, resp, m.Provider, stream, out)
}

func consumeProtocolResponse(w http.ResponseWriter, r *http.Request, resp *http.Response, protocolProvider string, stream bool, out result) result {
	if nativeEventStream(resp) {
		var final json.RawMessage
		err := consumeSSE(resp.Body, func(event string, payload []byte) error {
			if string(payload) == "[DONE]" {
				return nil
			}
			var data map[string]json.RawMessage
			if json.Unmarshal(payload, &data) != nil {
				return errors.New("上游 SSE 格式错误")
			}
			kind := event
			var t string
			json.Unmarshal(data["type"], &t)
			if t != "" {
				kind = t
			}
			if protocolProvider == "claude" {
				if kind == "message_start" {
					var message map[string]json.RawMessage
					json.Unmarshal(data["message"], &message)
					if u, ok := message["usage"]; ok {
						mergeNativeUsage(u, "claude", &out)
					}
				}
				if raw, ok := data["usage"]; ok {
					mergeNativeUsage(raw, "claude", &out)
				}
				if kind == "message_stop" {
					out.complete = true
				}
			} else if len(data["response"]) > 0 {
				var response map[string]json.RawMessage
				json.Unmarshal(data["response"], &response)
				if raw, ok := response["usage"]; ok {
					mergeNativeUsage(raw, "openai", &out)
				}
				if kind == "response.completed" {
					out.complete = true
					final = data["response"]
				}
			}
			if kind == "error" || kind == "response.failed" || kind == "response.incomplete" {
				applyStreamSignal(&out, payload)
				return errors.New("上游流式请求失败或未完成")
			}
			if stream {
				if !out.sent {
					w.Header().Set("Content-Type", "text/event-stream")
					w.Header().Set("Cache-Control", "no-cache")
					w.Header().Set("X-Accel-Buffering", "no")
					w.WriteHeader(200)
					out.sent = true
				}
				return emitSSE(w, event, payload)
			}
			return nil
		})
		if err != nil || !out.complete {
			out.complete = false
			out.err = errors.New("上游流被中断或缺少完成事件")
			if out.sent {
				emitSSE(w, "error", []byte(`{"type":"error","error":{"type":"upstream_error","message":"Upstream stream incomplete"}}`))
			}
			return out
		}
		if !stream {
			if protocolProvider == "openai" && len(final) > 0 {
				w.Header().Set("Content-Type", "application/json")
				w.Write(final)
				out.sent = true
			} else {
				out.err = errors.New("上游未返回所需非流式结果")
				return out
			}
		}
		out.status = 200
		return out
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if err != nil || len(data) >= 16<<20 {
		out.err = errors.New("上游响应读取失败或过大")
		return out
	}
	var root map[string]json.RawMessage
	if json.Unmarshal(data, &root) != nil {
		out.err = errors.New("上游响应格式错误")
		return out
	}
	if raw, ok := root["usage"]; ok {
		mergeNativeUsage(raw, protocolProvider, &out)
	}
	if _, hasError := root["error"]; hasError {
		out.err = errors.New("上游返回错误对象")
		return out
	}
	var status string
	json.Unmarshal(root["status"], &status)
	if status == "failed" || status == "incomplete" || status == "queued" || status == "in_progress" {
		out.err = errors.New("上游响应尚未完成")
		return out
	}
	out.complete = true
	out.status = 200
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(200)
	_, err = w.Write(data)
	out.sent = true
	if err != nil {
		out.err = errors.New("客户端连接已关闭")
	}
	return out
}

func mergeNativeUsage(raw []byte, provider string, out *result) {
	var v struct {
		Input   *int64 `json:"input_tokens"`
		Output  *int64 `json:"output_tokens"`
		Read    *int64 `json:"cache_read_input_tokens"`
		Write   *int64 `json:"cache_creation_input_tokens"`
		Details struct {
			Cached *int64 `json:"cached_tokens"`
		} `json:"input_tokens_details"`
	}
	if json.Unmarshal(raw, &v) != nil {
		out.known = false
		out.invalidUsage = true
		return
	}
	for _, n := range []*int64{v.Input, v.Output, v.Read, v.Write, v.Details.Cached} {
		if n != nil && *n < 0 {
			out.known = false
			out.invalidUsage = true
			return
		}
	}
	if provider == "openai" && v.Input != nil && v.Details.Cached != nil && *v.Details.Cached > *v.Input {
		out.invalidUsage = true
		out.known = false
		return
	}
	if v.Input != nil {
		out.seenInput = true
	}
	if v.Output != nil {
		out.seenOutput = true
	}
	out.known = out.seenInput && out.seenOutput && !out.invalidUsage
	if v.Input != nil {
		out.inputTotal = max(out.inputTotal, *v.Input)
		out.usage.Input = out.inputTotal
	}
	if v.Output != nil {
		out.usage.Output = max(out.usage.Output, *v.Output)
	}
	if v.Read != nil {
		out.usage.Cached = max(out.usage.Cached, *v.Read)
	}
	if v.Details.Cached != nil {
		out.usage.Cached = max(out.usage.Cached, *v.Details.Cached)
	}
	if v.Write != nil {
		out.usage.CacheWrite = max(out.usage.CacheWrite, *v.Write)
	}
	if provider == "openai" {
		if out.seenInput && out.usage.Cached > out.inputTotal {
			out.invalidUsage = true
			out.known = false
			return
		}
		out.usage.Input = max(0, out.inputTotal-out.usage.Cached)
	}
}

// Some Responses upstreams return valid SSE with an absent or generic content
// type. Inspect only a bounded prefix, preserve every byte, and still require
// valid events plus an explicit completion in consumeProtocolResponse.
func nativeEventStream(resp *http.Response) bool {
	media, _, _ := mime.ParseMediaType(resp.Header.Get("Content-Type"))
	if media == "text/event-stream" {
		return true
	}
	if resp.Header.Get("Content-Type") != "" && media != "text/plain" && media != "application/octet-stream" {
		return false
	}
	reader := bufio.NewReader(resp.Body)
	prefix, _ := reader.Peek(6)
	resp.Body = struct {
		io.Reader
		io.Closer
	}{reader, resp.Body}
	return bytes.HasPrefix(prefix, []byte("event:")) || bytes.HasPrefix(prefix, []byte("data:"))
}
