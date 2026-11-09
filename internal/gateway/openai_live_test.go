package gateway

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"fiftyapi/internal/accounts"
	"fiftyapi/internal/credentials"
	"fiftyapi/internal/providers"
	"fiftyapi/internal/store"
	ws "github.com/coder/websocket"
)

type openAILiveTransport struct {
	base http.RoundTripper
	t    *testing.T
}

func (a openAILiveTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	resp, err := a.base.RoundTrip(req)
	if err != nil {
		a.t.Log("upstream connection failed; raw error omitted")
		return nil, err
	}
	if req.Method == http.MethodPost && strings.Contains(req.URL.Path, "responses") {
		reader := bufio.NewReader(resp.Body)
		prefix, _ := reader.Peek(64)
		a.t.Logf("upstream response: HTTP %d, SSE_header=%t, JSON_header=%t, HTML_prefix=%t, SSE_prefix=%t", resp.StatusCode, strings.Contains(resp.Header.Get("Content-Type"), "text/event-stream"), strings.Contains(resp.Header.Get("Content-Type"), "application/json"), bytes.HasPrefix(bytes.TrimSpace(prefix), []byte("<")), bytes.HasPrefix(bytes.TrimSpace(prefix), []byte("event:")) || bytes.HasPrefix(bytes.TrimSpace(prefix), []byte("data:")))
		resp.Body = struct {
			io.Reader
			io.Closer
		}{reader, resp.Body}
	}
	return resp, nil
}

func (a openAILiveTransport) CloseIdleConnections() {
	if closer, ok := a.base.(interface{ CloseIdleConnections() }); ok {
		closer.CloseIdleConnections()
	}
}

// TestOpenAILive is an explicit, billable opt-in. The source DB is read-only;
// refresh tokens are removed before importing into a disposable test database.
// Never print credentials, response bodies, reasoning, or tool call IDs.
func TestOpenAILive(t *testing.T) {
	if os.Getenv("FIFTYAPI_TEST_OPENAI_LIVE") != "1" {
		t.Skip("set FIFTYAPI_TEST_OPENAI_LIVE=1 and explicit source paths/model to enable billable live acceptance")
	}
	dbPath, keyPath, model := os.Getenv("FIFTYAPI_TEST_OPENAI_DB"), os.Getenv("FIFTYAPI_TEST_OPENAI_KEY_FILE"), os.Getenv("FIFTYAPI_TEST_OPENAI_MODEL")
	account, err := strconv.ParseInt(os.Getenv("FIFTYAPI_TEST_OPENAI_ACCOUNT"), 10, 64)
	if err != nil || account < 1 || !filepath.IsAbs(dbPath) || !filepath.IsAbs(keyPath) || model == "" {
		t.Fatal("explicit absolute DB/key paths, account ID and model are required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Minute)
	defer cancel()
	sourceURL := &url.URL{Scheme: "file", Path: dbPath, RawQuery: "mode=ro"}
	source, err := sql.Open("sqlite", sourceURL.String())
	if err != nil {
		t.Fatal("cannot open source DB read-only")
	}
	defer source.Close()
	var encrypted []byte
	if err = source.QueryRowContext(ctx, "SELECT credentials FROM upstream_accounts WHERE id=? AND provider='openai' AND enabled=1", account).Scan(&encrypted); err != nil {
		t.Fatal("selected enabled OpenAI account not found")
	}
	keyText, err := os.ReadFile(keyPath)
	if err != nil {
		t.Fatal("cannot read source key file")
	}
	sourceKey, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(keyText)))
	if err != nil {
		t.Fatal("invalid source key encoding")
	}
	plain, err := credentials.Decrypt(sourceKey, fmt.Sprintf("fiftyapi:upstream:%d", account), encrypted)
	if err != nil {
		t.Fatal("cannot decrypt selected account")
	}
	var credential accounts.Credential
	if json.Unmarshal(plain, &credential) != nil || credential.Config.Provider != "openai" {
		t.Fatal("invalid OpenAI credential")
	}
	clear(plain)
	clear(sourceKey)
	clear(keyText)
	if time.Until(credential.ExpiresAt) < 10*time.Minute {
		t.Fatal("access token expires too soon; verify the account in the running service first, then retry")
	}
	credential.Token.RefreshToken = "" // Do not rotate a production refresh token from an isolated test.
	credential.Config.TimeoutSeconds = 90
	s, err := store.Open(ctx, filepath.Join(t.TempDir(), "acceptance.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	user, err := s.CreateAdmin(ctx, "live-acceptance", "unused-password-hash")
	if err != nil {
		t.Fatal(err)
	}
	if err = s.SetLimits(ctx, user, user, 1000000, 1000000); err != nil {
		t.Fatal(err)
	}
	_, apiKey, err := s.CreateAPIKey(ctx, user, "temporary-live-test")
	if err != nil {
		t.Fatal(err)
	}
	temporaryKey := make([]byte, 32)
	if _, err = rand.Read(temporaryKey); err != nil {
		t.Fatal(err)
	}
	service, err := accounts.New(s, temporaryKey)
	if err != nil {
		t.Fatal(err)
	}
	id, err := service.Import(ctx, user, "temporary-openai", credential)
	if err != nil {
		t.Fatal("cannot import temporary account")
	}
	var names []string
	// Only read-only discovery is retried; inference is never replayed.
	for attempt := 0; attempt < 3; attempt++ {
		names, err = service.Verify(ctx, id)
		if err == nil {
			break
		}
		if attempt < 2 {
			select {
			case <-ctx.Done():
				t.Fatal("catalog timeout")
			case <-time.After(time.Duration(attempt+1) * time.Second):
			}
		}
	}
	if err != nil {
		t.Fatalf("live catalog: %v", err)
	}
	found := false
	for _, name := range names {
		if name == model {
			found = true
		}
	}
	if !found {
		t.Fatal("explicit model is absent from the live catalog; no inference sent")
	}
	// Internal test price: 1 microUSD for every reported token, including cached
	// input. This is never represented as supplier pricing and never touches the
	// production model catalog or user balances.
	price := store.Price{Input: 1000000, Output: 1000000, Cached: 1000000, CacheWrite: 1000000}
	if err = s.SetModel(ctx, user, model, true, &price); err != nil {
		t.Fatal(err)
	}
	h := New(s, service)
	h.client = func(c providers.Config) *http.Client {
		client := providers.HTTPClient(c)
		client.Transport = openAILiveTransport{base: client.Transport, t: t}
		return client
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/models", h.Models)
	mux.HandleFunc("POST /v1/responses", h.Serve)
	mux.HandleFunc("POST /v1/responses/compact", h.Serve)
	var websocketRequests atomic.Int32
	mux.HandleFunc("GET /v1/responses", func(w http.ResponseWriter, r *http.Request) { websocketRequests.Add(1); h.ServeWebSocket(w, r) })
	server := httptest.NewServer(mux)
	defer server.Close()
	client := server.Client()
	client.Timeout = 100 * time.Second
	send := func(c context.Context, path string, body any) *http.Response {
		t.Helper()
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatal("cannot encode probe")
		}
		req, err := http.NewRequestWithContext(c, http.MethodPost, server.URL+path, bytes.NewReader(raw))
		if err != nil {
			t.Fatal("cannot create probe")
		}
		req.Header.Set("Authorization", "Bearer "+apiKey)
		req.Header.Set("Content-Type", "application/json")
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("local request failed (%T); credentials and body omitted", err)
		}
		if resp.StatusCode != 200 {
			resp.Body.Close()
			logs, _ := s.Logs(ctx, user, false, 0, "", 0)
			if len(logs) > 0 {
				t.Logf("local gateway diagnostic: status=%d, reason=%s", logs[0].Status, logs[0].Error)
			}
			t.Fatalf("live request failed: HTTP %d; body omitted", resp.StatusCode)
		}
		return resp
	}
	type response struct {
		Status string            `json:"status"`
		Output []json.RawMessage `json:"output"`
		Usage  struct {
			Input   int64 `json:"input_tokens"`
			Output  int64 `json:"output_tokens"`
			Details struct {
				Cached int64 `json:"cached_tokens"`
			} `json:"input_tokens_details"`
		} `json:"usage"`
	}
	var expected int64
	read := func(resp *http.Response, stream bool) response {
		t.Helper()
		defer resp.Body.Close()
		var out response
		if stream {
			completed := false
			err := consumeSSE(io.LimitReader(resp.Body, 8<<20), func(_ string, b []byte) error {
				if string(b) == "[DONE]" {
					return nil
				}
				var event struct {
					Type     string          `json:"type"`
					Response json.RawMessage `json:"response"`
				}
				if json.Unmarshal(b, &event) != nil {
					return fmt.Errorf("invalid event")
				}
				if event.Type == "error" || event.Type == "response.failed" || event.Type == "response.incomplete" {
					return fmt.Errorf("failed or incomplete stream")
				}
				if event.Type == "response.completed" {
					if json.Unmarshal(event.Response, &out) != nil {
						return fmt.Errorf("invalid completion")
					}
					completed = true
				}
				return nil
			})
			if err != nil || !completed {
				t.Fatal("live SSE did not complete; body omitted")
			}
		} else if json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(&out) != nil {
			t.Fatal("invalid JSON response")
		}
		if out.Status != "completed" || out.Usage.Input <= 0 || out.Usage.Output <= 0 || out.Usage.Details.Cached > out.Usage.Input {
			t.Fatal("response status or usage invalid")
		}
		expected += out.Usage.Input + out.Usage.Output
		return out
	}
	text := func(out response) string {
		var b strings.Builder
		for _, raw := range out.Output {
			var item struct {
				Content []struct {
					Type string `json:"type"`
					Text string `json:"text"`
				} `json:"content"`
			}
			json.Unmarshal(raw, &item)
			for _, part := range item.Content {
				if part.Type == "output_text" {
					b.WriteString(part.Text)
				}
			}
		}
		return strings.TrimSpace(b.String())
	}
	body := func(input any, stream bool) map[string]any {
		return map[string]any{"model": model, "input": input, "stream": stream, "max_output_tokens": 128, "reasoning": map[string]string{"effort": "low"}}
	}
	// The manifest must retain native Codex metadata, rather than synthesizing it.
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, server.URL+"/v1/models?client_version="+providers.DefaultOpenAIClientVersion, nil)
	req.Header.Set("Authorization", "Bearer "+apiKey)
	manifest, err := client.Do(req)
	if err != nil {
		t.Fatal("manifest request failed")
	}
	var catalog struct {
		Models []struct {
			Slug string `json:"slug"`
		} `json:"models"`
	}
	manifestBytes, readErr := io.ReadAll(io.LimitReader(manifest.Body, 8<<20))
	manifest.Body.Close()
	err = json.Unmarshal(manifestBytes, &catalog)
	if readErr != nil {
		t.Fatal("manifest read failed")
	}
	if manifest.StatusCode != 200 {
		var failure struct {
			Error struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		_ = json.Unmarshal(manifestBytes, &failure)
		for _, safe := range []string{"上游拒绝模型目录请求", "模型目录格式错误", "上游未返回 Codex 模型元数据；通用模型目录不能代替 Codex manifest", "无法获取 Codex 模型目录", "没有已定价的 Responses 模型或可用账号"} {
			if failure.Error.Message == safe {
				t.Fatalf("live manifest: %s", safe)
			}
		}
	}
	manifestHasModel := false
	for _, entry := range catalog.Models {
		if entry.Slug == model {
			manifestHasModel = true
		}
		m, _, e := s.RoutableModel(ctx, entry.Slug)
		if e != nil || !m.Enabled {
			t.Fatal("manifest contains an unavailable model")
		}
	}
	if err != nil || manifest.StatusCode != 200 || !manifestHasModel {
		t.Fatalf("filtered native Codex manifest invalid: HTTP %d, entries=%d, selected_present=%t; body omitted", manifest.StatusCode, len(catalog.Models), manifestHasModel)
	}
	t.Logf("live catalog and Codex manifest passed; catalog=%d, selected=%s", len(names), model)
	if binary := os.Getenv("FIFTYAPI_TEST_CODEX_CLI"); binary != "" {
		runNativeCodexAcceptance(t, ctx, binary, server.URL, apiKey, model, &websocketRequests, s, user)
		return
	}
	out := read(send(ctx, "/v1/responses", body("Reply with exactly FIFTYAPI_OPENAI_OK and no explanation.", false)), false)
	if text(out) != "FIFTYAPI_OPENAI_OK" {
		t.Fatal("text marker mismatch; body omitted")
	}
	t.Log("non-streaming text passed")
	first := json.RawMessage(`{"role":"user","content":"Call fiftyapi_probe_add with a=2 and b=3 exactly once. After receiving its result, reply with exactly 5 and no explanation."}`)
	tool := map[string]any{"type": "function", "name": "fiftyapi_probe_add", "description": "A fixed connectivity test: add 2 and 3.", "parameters": map[string]any{"type": "object", "properties": map[string]any{"a": map[string]string{"type": "integer"}, "b": map[string]string{"type": "integer"}}, "required": []string{"a", "b"}, "additionalProperties": false}, "strict": true}
	toolBody := body([]json.RawMessage{first}, true)
	toolBody["tools"] = []any{tool}
	toolBody["tool_choice"] = map[string]string{"type": "function", "name": "fiftyapi_probe_add"}
	toolBody["parallel_tool_calls"] = false
	call := read(send(ctx, "/v1/responses", toolBody), true)
	calls := 0
	callID := ""
	for _, raw := range call.Output {
		var item struct {
			Type      string `json:"type"`
			Name      string `json:"name"`
			CallID    string `json:"call_id"`
			Arguments string `json:"arguments"`
		}
		if json.Unmarshal(raw, &item) != nil {
			t.Fatal("invalid tool item")
		}
		if item.Type != "function_call" {
			continue
		}
		calls++
		var args struct {
			A *int `json:"a"`
			B *int `json:"b"`
		}
		if item.Name != "fiftyapi_probe_add" || item.CallID == "" || json.Unmarshal([]byte(item.Arguments), &args) != nil || args.A == nil || args.B == nil || *args.A != 2 || *args.B != 3 {
			t.Fatal("unexpected tool call; no tool executed")
		}
		callID = item.CallID
	}
	if calls != 1 {
		t.Fatal("expected exactly one fixed tool call")
	}
	history := append([]json.RawMessage{first}, call.Output...)
	result, _ := json.Marshal(map[string]string{"type": "function_call_output", "call_id": callID, "output": "5"})
	history = append(history, result)
	follow := body(history, true)
	follow["tools"] = []any{tool}
	follow["tool_choice"] = "none"
	final := read(send(ctx, "/v1/responses", follow), true)
	if text(final) != "5" {
		t.Fatal("tool result marker mismatch; body omitted")
	}
	t.Log("streaming tool call and multi-turn result passed")
	// Cancel only a fixed, bounded response, then wait for accounting to finish.
	cancelCtx, abort := context.WithCancel(ctx)
	resp := send(cancelCtx, "/v1/responses", body("Write the integers from 1 to 40, separated by spaces, with no explanation.", true))
	requestID := resp.Header.Get("X-Request-ID")
	sawText := false
	_ = consumeSSE(io.LimitReader(resp.Body, 1<<20), func(_ string, b []byte) error {
		var e struct {
			Type string `json:"type"`
		}
		if json.Unmarshal(b, &e) == nil && e.Type == "response.output_text.delta" {
			sawText = true
			abort()
			return context.Canceled
		}
		return nil
	})
	abort()
	resp.Body.Close()
	if !sawText || requestID == "" {
		t.Fatal("could not cancel after first text delta")
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		var status string
		err = s.DB.QueryRowContext(ctx, "SELECT status FROM request_charges WHERE id=?", requestID).Scan(&status)
		if err == nil && status == "settled" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("canceled request did not settle")
		}
		time.Sleep(25 * time.Millisecond)
	}
	logs, err := s.Logs(ctx, user, false, 0, "", 0)
	if err != nil || len(logs) != 4 {
		t.Fatalf("expected four isolated inference logs, got %d", len(logs))
	}
	var total, canceled int64
	for _, l := range logs {
		if l.Amount == nil {
			t.Fatal("unsettled request log")
		}
		total += *l.Amount
		if l.ID == requestID {
			canceled = *l.Amount
		} else if l.Status != 200 || l.Input == nil || l.Output == nil || *l.Amount != *l.Input+*l.Output+func() int64 {
			if l.Cached != nil {
				return *l.Cached
			}
			return 0
		}() {
			t.Fatal("completed request accounting mismatch")
		}
	}
	if total != expected+canceled {
		t.Fatal("upstream usage and local accounting mismatch")
	}
	quota, err := s.Quota(ctx, user)
	if err != nil || len(quota) != 2 {
		t.Fatal("quota read failed")
	}
	for _, q := range quota {
		if q.Used != total || q.Reserved != 0 {
			t.Fatal("quota or reservation mismatch")
		}
	}
	pending, err := s.PendingCharges(ctx)
	if err != nil || len(pending) != 0 {
		t.Fatal("unexpected pending charges")
	}
	t.Logf("cancellation and accounting passed: requests=%d, test_microUSD=%d, canceled_microUSD=%d, reservations=0; production balances unchanged", len(logs), total, canceled)
	// Optional compact is evaluated after core acceptance so unsupported upstreams
	// do not prevent cancellation and balance checks. A 404 remains a failure.
	if os.Getenv("FIFTYAPI_TEST_OPENAI_COMPACT") == "1" {
		compactHistory := append(append([]json.RawMessage{}, history...), final.Output...)
		compactBody := map[string]any{"model": model, "input": compactHistory, "instructions": "Preserve the fixed addition result and summarize the conversation briefly."}
		compactResp := send(ctx, "/v1/responses/compact", compactBody)
		var compact struct {
			Object string `json:"object"`
			Output []struct {
				Type      string `json:"type"`
				Encrypted string `json:"encrypted_content"`
			} `json:"output"`
			Usage struct {
				Input  int64 `json:"input_tokens"`
				Output int64 `json:"output_tokens"`
			} `json:"usage"`
		}
		decodeErr := json.NewDecoder(io.LimitReader(compactResp.Body, 8<<20)).Decode(&compact)
		compactResp.Body.Close()
		hasCompaction := false
		for _, item := range compact.Output {
			if item.Type == "compaction" && item.Encrypted != "" {
				hasCompaction = true
			}
		}
		if decodeErr != nil || compact.Object != "response.compaction" || !hasCompaction || compact.Usage.Input <= 0 || compact.Usage.Output <= 0 {
			t.Fatal("live compact response invalid; body omitted")
		}
		expected += compact.Usage.Input + compact.Usage.Output
		q, e := s.Quota(ctx, user)
		if e != nil || len(q) != 2 {
			t.Fatal("compact quota read failed")
		}
		for _, window := range q {
			if window.Used != expected+canceled || window.Reserved != 0 {
				t.Fatal("compact accounting mismatch")
			}
		}
		t.Log("native compact and accounting passed")
	}

	if os.Getenv("FIFTYAPI_TEST_OPENAI_WS_LIVE") == "1" {
		wsContext, stopWS := context.WithTimeout(ctx, 2*time.Minute)
		defer stopWS()
		headers := http.Header{"Authorization": []string{"Bearer " + apiKey}}
		conn, upgradeResponse, err := ws.Dial(wsContext, "ws"+strings.TrimPrefix(server.URL, "http")+"/v1/responses", &ws.DialOptions{HTTPHeader: headers})
		if err != nil {
			status := 0
			if upgradeResponse != nil {
				status = upgradeResponse.StatusCode
			}
			t.Fatalf("live websocket upgrade failed: HTTP %d", status)
		}
		defer conn.CloseNow()
		lastWSResponseID := ""
		readResponse := func() response {
			t.Helper()
			for {
				_, raw, e := conn.Read(wsContext)
				if e != nil {
					t.Fatal("live websocket closed before terminal event; details omitted")
				}
				var event struct {
					Type     string          `json:"type"`
					Response json.RawMessage `json:"response"`
					Error    struct {
						Code string `json:"code"`
					} `json:"error"`
				}
				if json.Unmarshal(raw, &event) != nil {
					t.Fatal("invalid websocket event")
				}
				if event.Type == "error" || event.Type == "response.failed" || event.Type == "response.incomplete" {
					t.Fatal("live websocket reported a protocol/upstream error; body omitted")
				}
				if event.Type == "response.completed" {
					var result response
					var identity struct {
						ID string `json:"id"`
					}
					_ = json.Unmarshal(event.Response, &identity)
					lastWSResponseID = identity.ID
					if json.Unmarshal(event.Response, &result) != nil {
						t.Fatal("invalid websocket completion")
					}
					return result
				}
			}
		}
		send := func(value any) {
			t.Helper()
			raw, _ := json.Marshal(value)
			if conn.Write(wsContext, ws.MessageText, raw) != nil {
				t.Fatal("live websocket write failed")
			}
		}
		send(map[string]any{"type": "response.create", "model": model, "generate": false, "input": "Reply with exactly FIFTYAPI_WS_OK.", "reasoning": map[string]string{"effort": "low"}})
		warm := readResponse()
		if text(warm) != "" || warm.Usage.Output != 0 {
			t.Fatal("warmup unexpectedly generated output")
		}
		// Response IDs remain private and are used only on this native connection.
		// The typed test projection intentionally omits IDs, so retain the ID via a
		// callback added below to readResponse.
		_ = warm
		previous := lastWSResponseID
		if previous == "" {
			t.Fatal("warmup did not return a response ID")
		}
		send(map[string]any{"type": "response.create", "model": model, "previous_response_id": previous, "input": []any{map[string]string{"role": "user", "content": "Reply with exactly FIFTYAPI_WS_OK."}}, "reasoning": map[string]string{"effort": "low"}})
		first := readResponse()
		if text(first) != "FIFTYAPI_WS_OK" {
			t.Fatal("live websocket text marker mismatch")
		}
		previous = lastWSResponseID
		send(map[string]any{"type": "response.create", "previous_response_id": previous, "input": []any{map[string]string{"role": "user", "content": "Reply with exactly FIFTYAPI_WS_NEXT."}}, "reasoning": map[string]string{"effort": "low"}})
		second := readResponse()
		if text(second) != "FIFTYAPI_WS_NEXT" {
			t.Fatal("live websocket continuation marker mismatch")
		}
		expectedWS := warm.Usage.Input + warm.Usage.Output + first.Usage.Input + first.Usage.Output + second.Usage.Input + second.Usage.Output
		deadline := time.Now().Add(5 * time.Second)
		for {
			q, e := s.Quota(ctx, user)
			if e == nil && len(q) == 2 && q[0].Reserved == 0 && q[1].Reserved == 0 && q[0].Used == total+expectedWS && q[1].Used == total+expectedWS {
				break
			}
			if time.Now().After(deadline) {
				t.Fatal("live websocket quota reconciliation failed")
			}
			time.Sleep(20 * time.Millisecond)
		}
		t.Logf("native websocket warmup, two chained responses and accounting passed: turns=3, test_microUSD=%d, reservations=0; production unchanged", expectedWS)
	}

}

// A fixed prompt in an empty temporary home. The child receives only a
// disposable gateway key, never source credentials or the developer's config.
// The optional workflow fixture remains inside the read-only temporary home and
// proves multiple native tool turns without placing credentials or repository
// contents in the child's working directory.
func runNativeCodexAcceptance(t *testing.T, ctx context.Context, binary, origin, key, model string, connections *atomic.Int32, s *store.Store, user int64) {
	t.Helper()
	if !filepath.IsAbs(binary) {
		t.Fatal("CLI binary must be an explicit absolute path")
	}
	home := t.TempDir()
	if err := os.Mkdir(filepath.Join(home, "codex"), 0700); err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(home, "answer.txt")
	prompt := "Reply with exactly FIFTYAPI_CODEX_OK. Do not call tools, read files, run commands, or add explanation."
	expected := "FIFTYAPI_CODEX_OK"
	minimumRequests := 1
	workflow := os.Getenv("FIFTYAPI_TEST_CODEX_WORKFLOW") == "1"
	if workflow {
		if err := os.Mkdir(filepath.Join(home, "nested"), 0700); err != nil {
			t.Fatal(err)
		}
		fixtures := map[string]string{
			"first.txt":        "Read second.txt in a new shell-tool turn. Do not guess the final marker.\n",
			"second.txt":       "Read nested/final.txt in a new shell-tool turn. Do not guess the final marker.\n",
			"nested/final.txt": "FIFTYAPI_CODEX_WORKFLOW_OK\n",
		}
		for name, content := range fixtures {
			if err := os.WriteFile(filepath.Join(home, name), []byte(content), 0600); err != nil {
				t.Fatal(err)
			}
		}
		prompt = "Use the shell tool in separate sequential turns. First read first.txt only and follow its instruction. Do not read ahead, combine commands, or inspect any other path. After reaching the final file, reply with its verification marker exactly and no explanation."
		expected = "FIFTYAPI_CODEX_WORKFLOW_OK"
		minimumRequests = 4
	}
	config := []string{`approval_policy="never"`, `model_provider="fiftyapi_acceptance"`, `model_reasoning_effort="low"`, `model_providers.fiftyapi_acceptance.name="FiftyAPI"`, `model_providers.fiftyapi_acceptance.base_url="` + origin + `/v1"`, `model_providers.fiftyapi_acceptance.env_key="FIFTYAPI_ACCEPTANCE_KEY"`, `model_providers.fiftyapi_acceptance.wire_api="responses"`, `model_providers.fiftyapi_acceptance.requires_openai_auth=false`, `model_providers.fiftyapi_acceptance.supports_websockets=true`}
	args := []string{"exec", "--skip-git-repo-check", "--ephemeral", "--ignore-user-config", "--ignore-rules", "--sandbox", "read-only", "-C", home, "-m", model, "-o", output}
	for _, c := range config {
		args = append(args, "-c", c)
	}
	args = append(args, prompt)
	child, stop := context.WithTimeout(ctx, 100*time.Second)
	defer stop()
	cmd := exec.CommandContext(child, binary, args...)
	cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "LANG=C.UTF-8", "HOME=" + home, "CODEX_HOME=" + filepath.Join(home, "codex"), "FIFTYAPI_ACCEPTANCE_KEY=" + key}
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	if err := cmd.Run(); err != nil {
		t.Fatalf("native Codex exited unsuccessfully (%T), websocket upgrades=%d; child output omitted", err, connections.Load())
	}
	answer, err := os.ReadFile(output)
	if err != nil || strings.TrimSpace(string(answer)) != expected {
		t.Fatal("native Codex marker mismatch; output omitted")
	}
	if connections.Load() == 0 {
		t.Fatal("native Codex completed through HTTP; WebSocket acceptance not proven")
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		var pending int
		if err = s.DB.QueryRowContext(ctx, "SELECT count(*) FROM request_charges WHERE status='reserved'").Scan(&pending); err != nil {
			t.Fatal(err)
		}
		if pending == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("native CLI left active reservations")
		}
		time.Sleep(20 * time.Millisecond)
	}
	logs, err := s.Logs(ctx, user, false, 0, "", 0)
	if err != nil || len(logs) < minimumRequests {
		t.Fatalf("native CLI produced too few request logs: got %d, want at least %d", len(logs), minimumRequests)
	}
	for _, l := range logs {
		if l.Status != 200 || l.Amount == nil {
			t.Fatal("native CLI request did not settle successfully")
		}
	}
	if workflow {
		t.Logf("native Codex isolated multi-tool WebSocket workflow passed: upgrades=%d, requests=%d, reservations=0; source DB unchanged", connections.Load(), len(logs))
	} else {
		t.Logf("native Codex fixed-prompt WebSocket acceptance passed: upgrades=%d, requests=%d, reservations=0; source DB unchanged", connections.Load(), len(logs))
	}
}
