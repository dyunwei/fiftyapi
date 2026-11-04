package providers

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestAntigravityInferenceRoundtrip(t *testing.T) {
	turns := 0
	signature := `{"functionCall":{"name":"probe_add","id":"call-1","args":{"a":2,"b":3}},"thoughtSignature":"preserve-verbatim"}`
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1internal:streamGenerateContent" || r.URL.Query().Get("alt") != "sse" {
			t.Error("incorrect inference URL")
		}
		if r.Header.Get("Authorization") != "Bearer access" || r.Header.Get("User-Agent") != "antigravity/hub/2.23.0 windows/amd64" {
			t.Error("incorrect auth or UA")
		}
		var envelope struct {
			Project, Model, UserAgent, RequestType, RequestID string
			Request                                           struct {
				Contents []geminiContent `json:"contents"`
				System   json.RawMessage `json:"systemInstruction"`
			}
		}
		if json.NewDecoder(r.Body).Decode(&envelope) != nil {
			t.Fatal("invalid request")
		}
		if envelope.Project != "existing" || envelope.Model != "gemini-3.5-flash-lite" || envelope.UserAgent != "antigravity" || envelope.RequestType != "agent" || !strings.HasPrefix(envelope.RequestID, "agent-") {
			t.Error("incorrect envelope")
		}
		if !strings.Contains(string(envelope.Request.System), "test identity") {
			t.Error("missing identity")
		}
		w.Header().Set("Content-Type", "text/event-stream")
		turns++
		if turns == 2 {
			fmt.Fprintf(w, "data: {\"response\":{\"candidates\":[{\"content\":{\"role\":\"model\",\"parts\":[%s]},\"finishReason\":\"STOP\"}],\"usageMetadata\":{\"promptTokenCount\":20,\"candidatesTokenCount\":4}}}\n\n", signature)
			return
		}
		text := "FIFTYAPI_OK"
		if turns == 3 {
			text = "5"
			if len(envelope.Request.Contents) != 3 {
				t.Error("missing history")
				return
			}
			if string(envelope.Request.Contents[1].Parts[0]) != signature {
				t.Error("signature or function call was modified")
			}
			raw := string(envelope.Request.Contents[2].Parts[0])
			if !strings.Contains(raw, `"id":"call-1"`) || !strings.Contains(raw, `"result":5`) {
				t.Error("missing tool response")
			}
		}
		fmt.Fprintf(w, "data: {\"response\":{\"candidates\":[{\"content\":{\"parts\":[{\"text\":%q}]}}]}}\n\n", text)
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		io.WriteString(w, "data: {\"response\":{\"candidates\":[{\"finishReason\":\"STOP\"}],\"usageMetadata\":{\"promptTokenCount\":10,\"candidatesTokenCount\":2,\"totalTokenCount\":12}}}\n\n")
	}))
	defer server.Close()
	c := config()
	c.Provider = "antigravity"
	c.ModelsURL = server.URL + "/models"
	c.ModelsBody = json.RawMessage(`{"project":"existing"}`)
	c.AntigravityIdentity = "test identity"
	results, err := CheckAntigravity(context.Background(), server.Client(), c, Token{AccessToken: "access"}, "gemini-3.5-flash-lite", true)
	if err != nil || turns != 3 || len(results) != 3 {
		t.Fatalf("roundtrip: %v turns=%d", err, turns)
	}
	if results[0].Text != "FIFTYAPI_OK" || results[2].Text != "5" || results[0].Usage.Input == nil || *results[0].Usage.Input != 10 {
		t.Fatal("incorrect result or usage")
	}
}

func TestGeminiSSEValidation(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		wantError  bool
	}{
		{"multiline", ":keepalive\r\ndata: {\"response\":\r\ndata: {\"candidates\":[{\"finishReason\":\"STOP\"}]}}\r\n\r\n", false},
		{"truncated", `data: {"response":{"candidates":[{"content":{"parts":[{"text":"partial"}]}}]}}` + "\n\n", true},
		{"error", `data: {"error":{"message":"private"}}` + "\n\n", true},
		{"malformed", "data: invalid\n\n", true},
		{"negative", `data: {"candidates":[{"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":-1}}` + "\n\n", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var result InferenceResult
			err := readGeminiSSE(strings.NewReader(tc.body), &result, time.Now())
			if (err != nil) != tc.wantError {
				t.Fatalf("got %v", err)
			}
			if err != nil && strings.Contains(err.Error(), "private") {
				t.Fatal("leaked raw error")
			}
		})
	}
}

func TestGeminiUsageSnapshotsAndHiddenThoughts(t *testing.T) {
	body := `data: {"response":{"candidates":[{"content":{"parts":[{"text":"hidden","thought":true},{"text":"OK"}]}}],"usageMetadata":{"promptTokenCount":10,"candidatesTokenCount":1}}}` + "\n\n" +
		`data: {"response":{"candidates":[{"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":10,"candidatesTokenCount":2,"thoughtsTokenCount":3}}}` + "\n\n"
	result := InferenceResult{FirstTextMillis: -1}
	err := readGeminiSSE(strings.NewReader(body), &result, time.Now())
	if err != nil || result.Text != "OK" || *result.Usage.Input != 10 || *result.Usage.Output != 2 || result.Usage.Cached != nil || result.Usage.Total != nil {
		t.Fatalf("invalid usage/text: %v", err)
	}
}

func TestProbeRejectsUnexpectedTool(t *testing.T) {
	for _, raw := range []string{`{"functionCall":{"name":"shell","args":{"command":"anything"}}}`, `{"functionCall":{"name":"probe_add","args":{"a":999,"b":3}}}`, `{"text":"no tool"}`} {
		if _, err := executeProbeTool([]json.RawMessage{json.RawMessage(raw)}); err == nil {
			t.Fatal("accepted unexpected tool")
		}
	}
}
