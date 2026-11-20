package gateway

import (
	"bytes"
	"encoding/json"
	"errors"
	"fiftyapi/internal/accounts"
	"fiftyapi/internal/providers"
	"fiftyapi/internal/store"
	"io"
	"net/http"
	"strings"
)

// Antigravity accepts its native v1internal envelope. No Messages/Gemini
// conversion, signature rewriting, tool emulation, or response aggregation.
func (h *Handler) antigravity(w http.ResponseWriter, r *http.Request, body []byte, m store.Model, c accounts.Credential, output int64) result {
	out := result{status: 502}
	var request map[string]json.RawMessage
	if json.Unmarshal(body, &request) != nil || len(request["request"]) == 0 {
		return result{status: 400, err: errors.New("Antigravity 需要原生 request 请求体")}
	}
	request["model"], _ = json.Marshal(m.UpstreamID)
	var project map[string]json.RawMessage
	_ = json.Unmarshal(c.Config.ModelsBody, &project)
	// Project is account-scoped, like authentication. A local caller cannot select
	// another account's project through its request body.
	if len(project["project"]) == 0 {
		return result{status: 400, err: errors.New("账号项目尚未就绪")}
	}
	request["project"] = project["project"]
	stream := strings.HasSuffix(r.URL.Path, ":streamGenerateContent")
	endpoint, err := providers.AntigravityInferenceEndpoint(c.Config, stream)
	if err != nil {
		out.err = err
		return out
	}
	encoded, _ := json.Marshal(request)
	req, err := http.NewRequestWithContext(r.Context(), http.MethodPost, endpoint, bytes.NewReader(encoded))
	if err != nil {
		out.err = err
		return out
	}
	req.Header.Set("Authorization", "Bearer "+c.Token.AccessToken)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	version := c.Config.AntigravityVersion
	if version == "" {
		version = "2.23.0"
	}
	req.Header.Set("User-Agent", "antigravity/hub/"+version+" windows/amd64")
	for k, v := range c.Config.GatewayHeaders {
		req.Header.Set(k, v)
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
	out.observeHTTPResponse(resp)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		out.status = resp.StatusCode
		if out.status == 400 || out.status == 401 || out.status == 403 || out.status == 404 || out.status == 429 || out.status == 508 {
			out.known = true
			out.complete = true
		}
		out.err = errors.New("上游拒绝请求，请检查模型权限或账号状态")
		classifyRejection(&out, resp)
		if out.status == http.StatusTooManyRequests {
			out.err = errors.New("上游返回 HTTP 429：请求受限，可能是限流、额度或模型容量限制")
		}
		return out
	}
	observeHeaders(&out, resp.Header)
	if stream {
		if !out.eventStream(resp) {
			out.err = errors.New("上游未返回 SSE 响应")
			return out
		}
		finished := errors.New("native generation completed")
		err = consumeSSE(resp.Body, func(event string, payload []byte) error {
			if string(payload) == "[DONE]" {
				return nil
			}
			if e := mergeAntigravity(payload, &out); e != nil {
				return e
			}
			if !out.sent {
				w.Header().Set("Content-Type", "text/event-stream")
				w.Header().Set("Cache-Control", "no-cache")
				w.WriteHeader(200)
				out.sent = true
			}
			if e := emitSSE(w, event, payload); e != nil {
				return e
			}
			if out.complete {
				return finished
			}
			return nil
		})
		if !errors.Is(err, finished) || !out.complete {
			out.complete = false
			out.err = errors.New("上游流未完整结束")
			return out
		}
	} else {
		data, e := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
		if e != nil || len(data) >= 16<<20 {
			out.err = errors.New("上游响应读取失败或过大")
			return out
		}
		if e = mergeAntigravity(data, &out); e != nil {
			out.err = e
			return out
		}
		out.upstreamTransport = "http_json"
		if !out.complete {
			out.err = errors.New("上游响应尚未完成")
			return out
		}
		w.Header().Set("Content-Type", "application/json")
		_, err = w.Write(data)
		out.sent = true
		if err != nil {
			out.err = err
			return out
		}
	}
	out.status = 200
	return out
}
func mergeAntigravity(payload []byte, out *result) error {
	var root map[string]json.RawMessage
	if json.Unmarshal(payload, &root) != nil {
		return errors.New("上游响应格式错误")
	}
	if len(root["error"]) > 0 {
		return errors.New("上游返回错误对象")
	}
	if len(root["response"]) > 0 {
		if json.Unmarshal(root["response"], &root) != nil {
			return errors.New("上游响应格式错误")
		}
	}
	var u struct {
		Input    *int64 `json:"promptTokenCount"`
		Output   *int64 `json:"candidatesTokenCount"`
		Cached   *int64 `json:"cachedContentTokenCount"`
		Thoughts *int64 `json:"thoughtsTokenCount"`
	}
	if raw := root["usageMetadata"]; len(raw) > 0 {
		if json.Unmarshal(raw, &u) != nil {
			out.invalidUsage = true
			out.known = false
		} else {
			for _, n := range []*int64{u.Input, u.Output, u.Cached, u.Thoughts} {
				if n != nil && *n < 0 {
					out.invalidUsage = true
				}
			}
			if u.Input != nil && u.Cached != nil && *u.Cached > *u.Input {
				out.invalidUsage = true
			}
			if u.Input != nil {
				out.inputTotal = max(out.inputTotal, *u.Input)
				out.seenInput = true
			}
			if u.Output != nil {
				n := *u.Output
				if u.Thoughts != nil {
					if *u.Thoughts > 9223372036854775807-n {
						out.invalidUsage = true
					} else {
						n += *u.Thoughts
					}
				}
				out.usage.Output = max(out.usage.Output, n)
				out.seenOutput = true
			}
			if u.Cached != nil {
				out.usage.Cached = max(out.usage.Cached, *u.Cached)
			}
			out.usage.Input = max(0, out.inputTotal-out.usage.Cached)
			out.usage.InputTotal = out.inputTotal
			out.known = out.seenInput && out.seenOutput && !out.invalidUsage
		}
	}
	var candidates []struct {
		Finish string `json:"finishReason"`
	}
	if json.Unmarshal(root["candidates"], &candidates) == nil && len(candidates) > 0 {
		out.complete = true
		for _, candidate := range candidates {
			if candidate.Finish == "" {
				out.complete = false
			}
		}
	}
	return nil
}
