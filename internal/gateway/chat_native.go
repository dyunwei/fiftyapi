package gateway

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
)

// Extract billing metadata only. Request fields, tool calls, reasoning and
// response JSON stay in Chat Completions format throughout the relay.
func mergeChatUsage(raw json.RawMessage, out *result) {
	if len(raw) == 0 || string(raw) == "null" {
		return
	}
	var usage struct {
		Prompt     *int64 `json:"prompt_tokens"`
		Completion *int64 `json:"completion_tokens"`
		Details    struct {
			Cached *int64 `json:"cached_tokens"`
		} `json:"prompt_tokens_details"`
	}
	if json.Unmarshal(raw, &usage) != nil {
		out.invalidUsage = true
		out.known = false
		return
	}
	converted, _ := json.Marshal(map[string]any{"input_tokens": usage.Prompt, "output_tokens": usage.Completion, "input_tokens_details": map[string]any{"cached_tokens": usage.Details.Cached}})
	mergeNativeUsage(converted, "openai", out)
}
func consumeChatResponse(w http.ResponseWriter, r *http.Request, resp *http.Response, stream bool, out result) result {
	if nativeEventStream(resp) {
		if !stream {
			out.err = errors.New("上游返回 SSE，但请求要求 Chat JSON；未转换响应")
			return out
		}
		completed := errors.New("chat stream completed")
		seenChoice := false
		err := consumeSSE(resp.Body, func(event string, payload []byte) error {
			if string(payload) == "[DONE]" {
				if !seenChoice {
					return errors.New("empty chat stream")
				}
				out.complete = true
				if err := emitSSE(w, "", payload); err != nil {
					return err
				}
				return completed
			}
			var frame struct {
				Choices []json.RawMessage `json:"choices"`
				Usage   json.RawMessage   `json:"usage"`
				Error   json.RawMessage   `json:"error"`
			}
			if json.Unmarshal(payload, &frame) != nil {
				return errors.New("invalid chat event")
			}
			mergeChatUsage(frame.Usage, &out)
			if len(frame.Error) > 0 && string(frame.Error) != "null" {
				applyStreamSignal(&out, payload)
				return errors.New("upstream chat error")
			}
			if len(frame.Choices) > 0 {
				seenChoice = true
			}
			if !out.sent {
				w.Header().Set("Content-Type", "text/event-stream")
				w.Header().Set("Cache-Control", "no-cache")
				w.Header().Set("X-Accel-Buffering", "no")
				w.WriteHeader(200)
				out.sent = true
			}
			return emitSSE(w, event, payload)
		})
		if !errors.Is(err, completed) || !out.complete {
			out.complete = false
			out.err = errors.New("上游 Chat 流被中断、返回错误或缺少完成标记")
			if out.sent {
				_ = emitSSE(w, "", []byte(`{"error":{"type":"upstream_error","message":"Upstream Chat stream incomplete"}}`))
			}
			return out
		}
		out.status = 200
		return out
	}
	if stream {
		out.err = errors.New("上游未返回要求的 Chat SSE；未转换响应")
		return out
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if err != nil || len(body) >= 16<<20 {
		out.err = errors.New("上游 Chat 响应读取失败或过大")
		return out
	}
	var response struct {
		Choices []json.RawMessage `json:"choices"`
		Usage   json.RawMessage   `json:"usage"`
		Error   json.RawMessage   `json:"error"`
	}
	if json.Unmarshal(body, &response) != nil {
		out.err = errors.New("上游 Chat 响应格式无效")
		return out
	}
	mergeChatUsage(response.Usage, &out)
	if len(response.Error) > 0 && string(response.Error) != "null" {
		applyStreamSignal(&out, body)
		out.err = errors.New("上游 Chat 返回错误对象")
		return out
	}
	if len(response.Choices) == 0 {
		out.err = errors.New("上游未返回 Chat choices；未转换其他协议响应")
		return out
	}
	out.complete = true
	w.Header().Set("Content-Type", "application/json")
	if _, err = w.Write(body); err != nil {
		out.err = errors.New("客户端响应写入失败")
		return out
	}
	out.sent = true
	out.status = 200
	return out
}
