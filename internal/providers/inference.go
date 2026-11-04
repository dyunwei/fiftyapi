package providers

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Usage fields remain nil if the upstream did not report them. Streaming
// counters are cumulative snapshots, so they must not be summed per event.
type Usage struct {
	Input    *int64 `json:"promptTokenCount"`
	Output   *int64 `json:"candidatesTokenCount"`
	Cached   *int64 `json:"cachedContentTokenCount"`
	Thoughts *int64 `json:"thoughtsTokenCount"`
	Total    *int64 `json:"totalTokenCount"`
}
type InferenceResult struct {
	Stage           string `json:"stage"`
	Model           string `json:"model"`
	Text            string `json:"text,omitempty"`
	FinishReason    string `json:"finish_reason"`
	Events          int    `json:"events"`
	FirstTextMillis int64  `json:"first_text_ms"`
	Usage           Usage  `json:"usage"`
	parts           []json.RawMessage
}

type geminiContent struct {
	Role  string            `json:"role"`
	Parts []json.RawMessage `json:"parts"`
}

func textContent(s string) geminiContent {
	b, _ := json.Marshal(map[string]string{"text": s})
	return geminiContent{Role: "user", Parts: []json.RawMessage{b}}
}

// CheckAntigravity performs one minimal streaming text turn and optionally a
// two-turn tool roundtrip. The only executable tool is a local 2+3 calculation.
// Model names are never silently substituted.
func CheckAntigravity(ctx context.Context, client *http.Client, c Config, t Token, model string, tools bool) ([]InferenceResult, error) {
	if c.Provider != "antigravity" || strings.TrimSpace(model) == "" {
		return nil, errors.New("推理探针需要 Antigravity 和明确的模型名")
	}
	if strings.TrimSpace(c.AntigravityIdentity) == "" {
		return nil, errors.New("缺少 Antigravity 身份提示配置，请用参考源码辅助脚本启动推理探针")
	}
	resolved, err := resolveAntigravityProject(ctx, client, c, t)
	if err != nil {
		return nil, err
	}
	first, err := antigravityTurn(ctx, client, resolved, t, model, []geminiContent{textContent("Reply with exactly FIFTYAPI_OK.")}, nil, "NONE")
	first.Stage = "text_stream"
	results := []InferenceResult{first}
	if err != nil {
		return results, err
	}
	if strings.TrimSpace(first.Text) != "FIFTYAPI_OK" {
		return results, errors.New("文本探针未返回预期标记")
	}
	if !tools {
		return results, nil
	}
	declaration := []any{map[string]any{"functionDeclarations": []any{map[string]any{"name": "probe_add", "description": "Add two integers for a connectivity test.", "parameters": map[string]any{"type": "OBJECT", "properties": map[string]any{"a": map[string]string{"type": "INTEGER"}, "b": map[string]string{"type": "INTEGER"}}, "required": []string{"a", "b"}}}}}}
	contents := []geminiContent{textContent("Call probe_add with a=2 and b=3. After receiving the tool result, reply with exactly 5.")}
	call, err := antigravityTurn(ctx, client, resolved, t, model, contents, declaration, "ANY")
	call.Stage = "tool_call"
	results = append(results, call)
	if err != nil {
		return results, err
	}
	responsePart, err := executeProbeTool(call.parts)
	if err != nil {
		return results, err
	}
	// Preserve every model part verbatim, including thoughtSignature and call ID.
	// No synthetic signatures are injected into returned model history.
	contents = append(contents, geminiContent{Role: "model", Parts: call.parts}, geminiContent{Role: "user", Parts: []json.RawMessage{responsePart}})
	final, err := antigravityTurn(ctx, client, resolved, t, model, contents, declaration, "NONE")
	final.Stage = "tool_result"
	results = append(results, final)
	if err != nil {
		return results, err
	}
	if strings.TrimSpace(final.Text) != "5" {
		return results, errors.New("工具结果回传后未得到预期答案")
	}
	return results, nil
}

func executeProbeTool(parts []json.RawMessage) (json.RawMessage, error) {
	var calls []struct {
		Name string `json:"name"`
		ID   string `json:"id"`
		Args struct {
			A *int `json:"a"`
			B *int `json:"b"`
		} `json:"args"`
	}
	for _, raw := range parts {
		var part struct {
			Call json.RawMessage `json:"functionCall"`
		}
		if json.Unmarshal(raw, &part) != nil {
			return nil, errors.New("工具调用 part 无效")
		}
		if len(part.Call) == 0 {
			continue
		}
		var call struct {
			Name string `json:"name"`
			ID   string `json:"id"`
			Args struct {
				A *int `json:"a"`
				B *int `json:"b"`
			} `json:"args"`
		}
		if json.Unmarshal(part.Call, &call) != nil {
			return nil, errors.New("工具调用参数无效")
		}
		calls = append(calls, call)
	}
	if len(calls) != 1 || calls[0].Name != "probe_add" || calls[0].Args.A == nil || calls[0].Args.B == nil || *calls[0].Args.A != 2 || *calls[0].Args.B != 3 {
		return nil, errors.New("模型未返回唯一的预期 probe_add(2,3) 调用；未执行其他工具")
	}
	response := map[string]any{"name": "probe_add", "response": map[string]int{"result": 5}}
	if calls[0].ID != "" {
		response["id"] = calls[0].ID
	}
	return json.Marshal(map[string]any{"functionResponse": response})
}

func antigravityTurn(ctx context.Context, client *http.Client, c Config, t Token, model string, contents []geminiContent, tools []any, mode string) (InferenceResult, error) {
	result := InferenceResult{Model: model, FirstTextMillis: -1}
	var modelBody struct {
		Project string `json:"project"`
	}
	if json.Unmarshal(c.ModelsBody, &modelBody) != nil || modelBody.Project == "" {
		return result, errors.New("推理请求缺少 project ID")
	}
	// Use an RFC 4122 UUID-shaped request ID, matching the reference envelope.
	uuidBytes := make([]byte, 16)
	if _, err := rand.Read(uuidBytes); err != nil {
		return result, err
	}
	uuidBytes[6] = (uuidBytes[6] & 0x0f) | 0x40
	uuidBytes[8] = (uuidBytes[8] & 0x3f) | 0x80
	requestID := fmt.Sprintf("agent-%x-%x-%x-%x-%x", uuidBytes[:4], uuidBytes[4:6], uuidBytes[6:8], uuidBytes[8:10], uuidBytes[10:])
	request := map[string]any{"contents": contents, "systemInstruction": map[string]any{"parts": []any{map[string]string{"text": c.AntigravityIdentity}}}, "generationConfig": map[string]any{"maxOutputTokens": 512}}
	if len(tools) > 0 {
		request["tools"] = tools
	}
	request["toolConfig"] = map[string]any{"functionCallingConfig": map[string]string{"mode": mode}}
	body, _ := json.Marshal(map[string]any{"project": modelBody.Project, "requestId": requestID, "userAgent": "antigravity", "requestType": "agent", "model": model, "request": request})
	u, err := url.Parse(c.ModelsURL)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil {
		return result, errors.New("推理端点无效")
	}
	// sub2api prioritizes daily for inference, production for project discovery.
	if u.Host == "cloudcode-pa.googleapis.com" {
		u.Host = "daily-cloudcode-pa.googleapis.com"
	}
	u.Path = "/v1internal:streamGenerateContent"
	u.RawPath = ""
	u.RawQuery = "alt=sse"
	u.Fragment = ""
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u.String(), bytes.NewReader(body))
	if err != nil {
		return result, errors.New("无法构造推理请求")
	}
	antigravityHeaders(req, c, t)
	req.Header.Set("Accept", "text/event-stream")
	start := time.Now()
	resp, err := client.Do(req)
	if err != nil {
		return result, &upstreamNetworkError{cause: err}
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, bodyLimit))
		return result, &UpstreamHTTPError{Status: resp.StatusCode, Code: safeErrorCode(b)}
	}
	media, _, _ := mime.ParseMediaType(resp.Header.Get("Content-Type"))
	if media != "text/event-stream" {
		return result, errors.New("推理返回非 SSE 响应，未输出原始内容")
	}
	err = readGeminiSSE(resp.Body, &result, start)
	return result, err
}

func readGeminiSSE(reader io.Reader, result *InferenceResult, start time.Time) error {
	limited := &io.LimitedReader{R: reader, N: bodyLimit + 1}
	scanner := bufio.NewScanner(limited)
	scanner.Buffer(make([]byte, 4096), 1<<20)
	var data strings.Builder
	consume := func() error {
		if data.Len() == 0 {
			return nil
		}
		payload := strings.TrimSuffix(data.String(), "\n")
		data.Reset()
		if payload == "[DONE]" {
			return nil
		}
		var outer map[string]json.RawMessage
		if json.Unmarshal([]byte(payload), &outer) != nil {
			return errors.New("SSE 事件 JSON 无效")
		}
		if _, ok := outer["error"]; ok {
			return errors.New("上游在 SSE 中返回错误，原始内容未输出")
		}
		raw := json.RawMessage(payload)
		if wrapped, ok := outer["response"]; ok {
			raw = wrapped
		}
		var chunk struct {
			Error      json.RawMessage `json:"error"`
			Candidates []struct {
				Index   int `json:"index"`
				Content struct {
					Parts []json.RawMessage `json:"parts"`
				} `json:"content"`
				Finish string `json:"finishReason"`
			} `json:"candidates"`
			Usage *Usage `json:"usageMetadata"`
		}
		if json.Unmarshal(raw, &chunk) != nil || len(chunk.Error) > 0 {
			return errors.New("SSE 模型响应无效")
		}
		result.Events++
		if chunk.Usage != nil {
			if err := mergeUsage(&result.Usage, *chunk.Usage); err != nil {
				return err
			}
		}
		for _, candidate := range chunk.Candidates {
			if candidate.Index != 0 {
				continue
			}
			if candidate.Finish != "" {
				result.FinishReason = candidate.Finish
			}
			for _, part := range candidate.Content.Parts {
				var fields struct {
					Text    string `json:"text"`
					Thought bool   `json:"thought"`
				}
				if json.Unmarshal(part, &fields) != nil {
					return errors.New("SSE part 无效")
				}
				result.parts = append(result.parts, append(json.RawMessage(nil), part...))
				if fields.Text != "" && !fields.Thought {
					if result.FirstTextMillis < 0 {
						result.FirstTextMillis = time.Since(start).Milliseconds()
					}
					result.Text += fields.Text
				}
			}
		}
		return nil
	}
	for scanner.Scan() {
		line := strings.TrimSuffix(scanner.Text(), "\r")
		if line == "" {
			if err := consume(); err != nil {
				return err
			}
			continue
		}
		if strings.HasPrefix(line, "data:") {
			value := strings.TrimPrefix(line, "data:")
			value = strings.TrimPrefix(value, " ")
			data.WriteString(value)
			data.WriteByte('\n')
		}
	}
	if scanner.Err() != nil {
		return errors.New("读取 SSE 失败或事件超过大小限制")
	}
	if limited.N <= 0 {
		return errors.New("SSE 响应超过总大小限制")
	}
	if err := consume(); err != nil {
		return err
	}
	if result.FinishReason != "STOP" {
		return errors.New("SSE 未正常完成：缺少 STOP 终止状态")
	}
	return nil
}

func mergeUsage(dst *Usage, src Usage) error {
	targets := []**int64{&dst.Input, &dst.Output, &dst.Cached, &dst.Thoughts, &dst.Total}
	values := []*int64{src.Input, src.Output, src.Cached, src.Thoughts, src.Total}
	for i, v := range values {
		if v == nil {
			continue
		}
		if *v < 0 {
			return errors.New("上游用量为负数")
		}
		if *targets[i] == nil || **targets[i] < *v {
			n := *v
			*targets[i] = &n
		}
	}
	return nil
}
