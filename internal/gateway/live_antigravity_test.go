package gateway

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fiftyapi/internal/accounts"
	"fiftyapi/internal/credentials"
	"fiftyapi/internal/providers"
	"fiftyapi/internal/store"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Opt-in only. Never run this with CI credentials or against a production DB.
// Refresh is saved back to the original encrypted session BEFORE importing a copy.
func TestLiveAntigravityMultiTurn(t *testing.T) {
	if os.Getenv("FIFTYAPI_LIVE_ANTIGRAVITY") != "1" {
		t.Skip("explicit live-test opt-in required")
	}
	root := filepath.Join("..", "..")
	session := filepath.Join(root, ".local", "antigravity.session")
	keyPath := filepath.Join(root, ".local", "probe.key")
	if _, err := os.Stat(keyPath); err != nil {
		t.Fatal("original probe key unavailable; no replacement generated")
	}
	key, err := credentials.LoadKey(keyPath)
	if err != nil {
		t.Fatal("cannot open original probe key")
	}
	raw, err := os.ReadFile(filepath.Join(root, "examples", "probe", "antigravity.json"))
	if err != nil {
		t.Fatal("probe config unavailable")
	}
	var cfg providers.Config
	if json.Unmarshal(raw, &cfg) != nil {
		t.Fatal("invalid probe config")
	}
	binding, _ := json.Marshal([]string{cfg.Provider, cfg.ClientID, cfg.AuthorizeURL, cfg.TokenURL, cfg.RedirectURI, cfg.Scope, cfg.ModelsURL})
	raw, err = credentials.Load(session, key, string(binding))
	if err != nil {
		t.Fatal("cannot decrypt original session")
	}
	var saved struct {
		Token     providers.Token `json:"token"`
		ExpiresAt time.Time       `json:"expires_at"`
	}
	if json.Unmarshal(raw, &saved) != nil {
		t.Fatal("invalid encrypted session")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	client := providers.HTTPClient(cfg)
	defer client.CloseIdleConnections()
	saved.Token, err = providers.Refresh(ctx, client, cfg, saved.Token, providers.ClientSecret(cfg))
	if err != nil {
		t.Fatal("live refresh failed; original session retained, inspect provider status without disclosing credentials")
	}
	saved.ExpiresAt = time.Now().Add(time.Duration(saved.Token.ExpiresIn) * time.Second)
	raw, _ = json.Marshal(saved)
	if credentials.Save(session, key, string(binding), raw) != nil {
		t.Fatal("refreshed token could not be persisted; stop testing")
	}
	cfg, err = providers.PrepareAntigravity(ctx, client, cfg, saved.Token)
	if err != nil {
		t.Fatal("project discovery failed after saved refresh")
	}
	models, err := providers.Models(ctx, client, cfg, saved.Token)
	if err != nil {
		t.Fatal("catalog discovery failed")
	}
	const model = "gemini-3.5-flash-lite"
	found := false
	for _, m := range models {
		if m == model {
			found = true
		}
	}
	if !found {
		t.Fatal("authorized test model absent from catalog; no substitute selected")
	}
	s, err := store.Open(ctx, filepath.Join(t.TempDir(), "live.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	user, err := s.CreateAdmin(ctx, "live-test", "not-a-login-hash")
	if err != nil {
		t.Fatal(err)
	}
	if err = s.SetLimits(ctx, user, user, 1000000, 1000000); err != nil {
		t.Fatal(err)
	}
	_, token, err := s.CreateAPIKey(ctx, user, "temporary-live-test")
	if err != nil {
		t.Fatal(err)
	}
	master := make([]byte, 32)
	if _, err = rand.Read(master); err != nil {
		t.Fatal(err)
	}
	service, err := accounts.New(s, master)
	if err != nil {
		t.Fatal(err)
	}
	id, err := service.Import(ctx, user, "temporary-live-account", accounts.Credential{Config: cfg, Token: saved.Token, ExpiresAt: saved.ExpiresAt})
	if err != nil {
		t.Fatal("temporary encrypted import failed")
	}
	if err = s.SyncModels(ctx, id, "antigravity", []string{model}); err != nil {
		t.Fatal(err)
	}
	if err = s.AccountState(ctx, id, "catalog_ready", ""); err != nil {
		t.Fatal(err)
	}
	if err = s.SetModel(ctx, user, model, true, &store.Price{Input: 1000000, Output: 1000000, Cached: 1000000, CacheWrite: 1000000}); err != nil {
		t.Fatal(err)
	}
	h := New(s, service)
	messages := []any{map[string]any{"role": "user", "content": "Call probe_add with a=2,b=3. After receiving its result call probe_add again with a=5,b=4. After the second result reply only 9."}}
	tools := []any{map[string]any{"name": "probe_add", "description": "Add two integers locally without side effects.", "input_schema": map[string]any{"type": "object", "properties": map[string]any{"a": map[string]string{"type": "integer"}, "b": map[string]string{"type": "integer"}}, "required": []string{"a", "b"}}}}
	var responses int
	for turn := 0; turn < 4; turn++ {
		request := map[string]any{"model": model, "stream": false, "max_tokens": 256, "messages": messages, "tools": tools}
		if turn < 2 {
			request["tool_choice"] = map[string]string{"type": "tool", "name": "probe_add"}
		} else {
			request["tool_choice"] = map[string]string{"type": "none"}
		}
		if turn == 3 {
			request["stream"] = true
		}
		body, _ := json.Marshal(request)
		r := httptest.NewRequest("POST", "/v1/messages", strings.NewReader(string(body))).WithContext(ctx)
		r.Header.Set("Authorization", "Bearer "+token)
		r.Header.Set("session_id", "live-multi-turn")
		w := httptest.NewRecorder()
		h.Serve(w, r)
		if w.Code != 200 {
			t.Fatalf("live turn %d failed with HTTP %d; response omitted", turn+1, w.Code)
		}
		if turn == 3 {
			var text strings.Builder
			err = consumeSSE(strings.NewReader(w.Body.String()), func(_ string, p []byte) error {
				var event struct {
					Delta struct {
						Text string `json:"text"`
					} `json:"delta"`
				}
				json.Unmarshal(p, &event)
				text.WriteString(event.Delta.Text)
				return nil
			})
			if err != nil || strings.TrimSpace(text.String()) != "9" || !strings.Contains(w.Body.String(), "message_stop") {
				t.Fatal("streaming follow-up did not preserve conversation result")
			}
		} else {
			var response struct {
				Content []json.RawMessage `json:"content"`
			}
			if json.Unmarshal(w.Body.Bytes(), &response) != nil {
				t.Fatal("invalid converted Messages response")
			}
			messages = append(messages, map[string]any{"role": "assistant", "content": response.Content})
			if turn < 2 {
				var toolID string
				var a, b int
				for _, block := range response.Content {
					var part struct {
						Type, ID, Name string
						Input          struct{ A, B int }
					}
					json.Unmarshal(block, &part)
					if part.Type == "tool_use" && part.Name == "probe_add" {
						toolID = part.ID
						a = part.Input.A
						b = part.Input.B
					}
				}
				if toolID == "" || (turn == 0 && (a != 2 || b != 3)) || (turn == 1 && (a != 5 || b != 4)) {
					t.Fatalf("turn %d did not produce expected local tool arguments", turn+1)
				}
				result := "5"
				if turn == 1 {
					result = "9"
				}
				messages = append(messages, map[string]any{"role": "user", "content": []any{map[string]any{"type": "tool_result", "tool_use_id": toolID, "content": result}}})
			} else {
				var text strings.Builder
				for _, block := range response.Content {
					var part struct{ Type, Text string }
					json.Unmarshal(block, &part)
					if part.Type == "text" {
						text.WriteString(part.Text)
					}
				}
				if strings.TrimSpace(text.String()) != "9" {
					t.Fatal("tool continuation did not return expected sum")
				}
				messages = append(messages, map[string]any{"role": "user", "content": "Repeat the final total only."})
			}
		}
		responses++
	}
	logs, err := s.Logs(ctx, user, false, 0, "", 0)
	if err != nil || len(logs) != responses {
		t.Fatal("live request log count mismatch")
	}
	var amount, tokens int64
	for _, l := range logs {
		if l.Amount == nil || l.Input == nil || l.Output == nil {
			t.Fatal("missing settled usage")
		}
		amount += *l.Amount
		tokens += *l.Input + *l.Output
		if l.Cached != nil {
			tokens += *l.Cached
		}
	}
	windows, err := s.Quota(ctx, user)
	if err != nil {
		t.Fatal(err)
	}
	for _, w := range windows {
		if w.Used != amount || w.Reserved != 0 {
			t.Fatal("live quota and log reconciliation failed")
		}
	}
	pending, err := s.PendingCharges(ctx)
	if err != nil || len(pending) != 0 {
		t.Fatal("unexpected pending live request")
	}
	t.Logf("live verification passed: model=%s, catalog=%d, requests=%d, reported_tokens=%d, test_microusd=%d; refresh persisted; two consecutive tools + result + streaming follow-up; both quotas/logs consistent; credentials omitted", model, len(models), responses, tokens, amount)
}
