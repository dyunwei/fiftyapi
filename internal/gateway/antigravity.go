package gateway

import (
	"bytes"
	"encoding/json"
	"errors"
	"fiftyapi/internal/accounts"
	compat "fiftyapi/internal/compat/antigravity"
	"fiftyapi/internal/store"
	"github.com/google/uuid"
	"io"
	"math"
	"net/http"
	"net/url"
	"strings"
	"time"
)

func (h *Handler) antigravity(w http.ResponseWriter, r *http.Request, body []byte, m store.Model, c accounts.Credential, output int64) result {
	out := result{status: 502}
	var input compat.ClaudeRequest
	if json.Unmarshal(body, &input) != nil {
		out.status = 400
		out.err = errors.New("Messages 请求无效")
		return out
	}
	if input.MaxTokens == 0 {
		input.MaxTokens = int(output)
	}
	var project struct {
		ID string `json:"project"`
	}
	if json.Unmarshal(c.Config.ModelsBody, &project) != nil || project.ID == "" {
		out.err = errors.New("账号项目尚未就绪")
		return out
	}
	opts := compat.DefaultTransformOptions()
	opts.MaxThinkingBudget = antigravityBudget(c.Config.InferencePolicy)
	if c.Config.IdentityPatchEnabled != nil {
		opts.EnableIdentityPatch = *c.Config.IdentityPatchEnabled
	}
	opts.IdentityPatch = c.Config.AntigravityIdentity
	wrapped, err := compat.TransformClaudeToGeminiWithOptions(&input, project.ID, m.UpstreamID, opts)
	if err != nil {
		out.status = 400
		out.err = errors.New("无法转换请求，请检查工具或思考配置")
		return out
	}
	var envelope struct {
		Model string `json:"model"`
	}
	json.Unmarshal(wrapped, &envelope)
	if envelope.Model != m.UpstreamID {
		out.status = 400
		out.err = errors.New("该内置工具需要切换模型，当前不允许静默改变模型与价格")
		return out
	}
	endpoint := c.Config.GatewayURL
	if endpoint == "" {
		u, _ := url.Parse(c.Config.ModelsURL)
		if u == nil {
			out.err = errors.New("账号端点无效")
			return out
		}
		if u.Host == "cloudcode-pa.googleapis.com" {
			u.Host = "daily-cloudcode-pa.googleapis.com"
		}
		u.Path = "/v1internal:streamGenerateContent"
		u.RawQuery = "alt=sse"
		endpoint = u.String()
	}
	req, err := http.NewRequestWithContext(r.Context(), http.MethodPost, endpoint, bytes.NewReader(wrapped))
	if err != nil {
		out.err = errors.New("无法构造上游请求")
		return out
	}
	u := req.URL
	if u.Scheme != "https" || u.User != nil {
		out.err = errors.New("上游端点必须使用 HTTPS")
		return out
	}
	req.Header.Set("Authorization", "Bearer "+c.Token.AccessToken)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")
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
		out.err = errors.New("上游连接失败")
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
	if !strings.Contains(resp.Header.Get("Content-Type"), "text/event-stream") {
		out.err = errors.New("上游未返回 SSE 响应")
		return out
	}
	processor := compat.NewStreamingProcessor(m.ID)
	var aggregate compat.GeminiResponse
	var aggregateParts []compat.GeminiPart
	responseID := ""
	finish := ""
	var prompt, outputTokens, cached, thoughts int64
	err = consumeSSE(resp.Body, func(_ string, payload []byte) error {
		if string(payload) == "[DONE]" {
			return nil
		}
		var root map[string]json.RawMessage
		if json.Unmarshal(payload, &root) != nil {
			return errors.New("上游 SSE JSON 无效")
		}
		if _, ok := root["error"]; ok {
			applyStreamSignal(&out, payload)
			return errors.New("上游返回流式错误")
		}
		raw := json.RawMessage(payload)
		if wrapped, ok := root["response"]; ok {
			raw = wrapped
		}
		var chunk compat.GeminiResponse
		if json.Unmarshal(raw, &chunk) != nil {
			return errors.New("上游响应无效")
		}
		var fields struct {
			Usage *struct {
				Prompt   *int64 `json:"promptTokenCount"`
				Output   *int64 `json:"candidatesTokenCount"`
				Cached   *int64 `json:"cachedContentTokenCount"`
				Thoughts *int64 `json:"thoughtsTokenCount"`
			} `json:"usageMetadata"`
		}
		json.Unmarshal(raw, &fields)
		if fields.Usage != nil {
			u := fields.Usage
			for _, n := range []*int64{u.Prompt, u.Output, u.Cached, u.Thoughts} {
				if n != nil && *n < 0 {
					out.invalidUsage = true
					out.known = false
					return errors.New("上游用量无效")
				}
			}
			if u.Prompt != nil {
				prompt = max(prompt, *u.Prompt)
				out.seenInput = true
			}
			if u.Output != nil {
				outputTokens = max(outputTokens, *u.Output)
				out.seenOutput = true
			}
			if u.Cached != nil {
				cached = max(cached, *u.Cached)
			}
			if u.Thoughts != nil {
				thoughts = max(thoughts, *u.Thoughts)
			}
			if (out.seenInput && cached > prompt) || outputTokens > math.MaxInt64-thoughts {
				out.invalidUsage = true
				out.known = false
				return errors.New("上游用量无效")
			}
			out.usage = store.TokenUsage{Input: max(0, prompt-cached), Output: outputTokens + thoughts, Cached: cached}
			out.known = out.seenInput && out.seenOutput && !out.invalidUsage
		}
		if chunk.ResponseID != "" {
			responseID = chunk.ResponseID
		}
		for _, candidate := range chunk.Candidates {
			if candidate.Index != 0 {
				continue
			}
			if candidate.Content != nil {
				aggregateParts = append(aggregateParts, candidate.Content.Parts...)
			}
			if candidate.FinishReason != "" {
				finish = candidate.FinishReason
				if finish == "MALFORMED_FUNCTION_CALL" {
					return errors.New("上游返回无效工具调用")
				}
			}
		}
		if input.Stream {
			normalized := payload
			if _, ok := root["response"]; !ok {
				normalized, _ = json.Marshal(map[string]any{"response": json.RawMessage(payload)})
			}
			data := processor.ProcessLine("data: " + string(normalized))
			if len(data) > 0 {
				if !out.sent {
					w.Header().Set("Content-Type", "text/event-stream")
					w.Header().Set("Cache-Control", "no-cache")
					w.Header().Set("X-Accel-Buffering", "no")
					w.WriteHeader(200)
					out.sent = true
				}
				http.NewResponseController(w).SetWriteDeadline(time.Now().Add(30 * time.Second))
				if _, err := w.Write(data); err != nil {
					return err
				}
				if f, ok := w.(http.Flusher); ok {
					f.Flush()
				}
			}
		}
		return nil
	})
	if err != nil || finish == "" {
		out.err = errors.New("上游流未完整结束")
		if out.sent {
			emitSSE(w, "error", []byte(`{"type":"error","error":{"type":"upstream_error","message":"Upstream stream incomplete"}}`))
		}
		return out
	}
	out.complete = true
	out.status = 200
	if input.Stream {
		tail, _ := processor.Finish()
		if len(tail) > 0 {
			w.Write(tail)
		}
		out.sent = true
		return out
	}
	aggregate.Candidates = []compat.GeminiCandidate{{Index: 0, FinishReason: finish, Content: &compat.GeminiContent{Role: "model", Parts: aggregateParts}}}
	aggregate.ResponseID = responseID
	if aggregate.ResponseID == "" {
		aggregate.ResponseID = "probe-" + uuid.NewString()
	}
	if out.known {
		aggregate.UsageMetadata = &compat.GeminiUsageMetadata{PromptTokenCount: int(prompt), CandidatesTokenCount: int(outputTokens), CachedContentTokenCount: int(cached), ThoughtsTokenCount: int(thoughts)}
	}
	raw, _ := json.Marshal(compat.V1InternalResponse{Response: aggregate})
	converted, _, err := compat.TransformGeminiToClaude(raw, m.ID)
	if err != nil {
		out.status = 502
		out.err = errors.New("无法转换上游回复")
		return out
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(200)
	_, err = io.Copy(w, bytes.NewReader(converted))
	out.sent = true
	if err != nil {
		out.err = errors.New("客户端连接已关闭")
	}
	return out
}
