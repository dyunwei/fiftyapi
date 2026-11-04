package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"fiftyapi/internal/accounts"
	"fiftyapi/internal/providers"
	"fiftyapi/internal/store"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type testGateway struct {
	h       *Handler
	s       *store.Store
	token   string
	user    int64
	account int64
}

func setupGateway(t *testing.T, provider string, upstream *httptest.Server) testGateway {
	t.Helper()
	ctx := context.Background()
	s, err := store.Open(ctx, filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	user, err := s.CreateAdmin(ctx, "admin", "hash")
	if err != nil {
		t.Fatal(err)
	}
	s.SetLimits(ctx, user, user, 10000000, 100000000)
	_, token, _ := s.CreateAPIKey(ctx, user, "test")
	service, err := accounts.New(s, make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	cfg := providers.Config{Provider: provider, ClientID: "test", Scope: "scope", AuthorizeURL: upstream.URL + "/authorize", TokenURL: upstream.URL + "/token", RedirectURI: "http://localhost/callback", TokenEncoding: "form", ModelsURL: upstream.URL + "/models", ModelsMethod: "POST", GatewayURL: upstream.URL + "/inference", ModelsBody: json.RawMessage(`{"project":"project"}`)}
	id, err := service.Import(ctx, user, "account", accounts.Credential{Config: cfg, Token: providers.Token{AccessToken: "upstream-private-token"}, ExpiresAt: time.Now().Add(time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.SyncModels(ctx, id, provider, []string{"model"}); err != nil {
		t.Fatal(err)
	}
	s.AccountState(ctx, id, "catalog_ready", "")
	s.SetModel(ctx, user, "model", true, &store.Price{Input: 1000000, Output: 1000000, Cached: 100000, CacheWrite: 1000000})
	h := New(s, service)
	h.client = func(providers.Config) *http.Client {
		c := *upstream.Client()
		c.Transport = c.Transport.(*http.Transport).Clone()
		c.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
		return &c
	}
	return testGateway{h: h, s: s, token: token, user: user, account: id}
}
func (g testGateway) request(path, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest("POST", path, strings.NewReader(body))
	r.Header.Set("Authorization", "Bearer "+g.token)
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	g.h.Serve(w, r)
	return w
}

func TestNativeGatewaySettlementAndTruncation(t *testing.T) {
	for _, provider := range []string{"claude", "openai"} {
		t.Run(provider, func(t *testing.T) {
			truncated := false
			upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Authorization") != "Bearer upstream-private-token" {
					t.Error("wrong upstream credential")
				}
				w.Header().Set("Content-Type", "text/event-stream")
				if provider == "claude" {
					io.WriteString(w, "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"usage\":{\"input_tokens\":10,\"output_tokens\":0}}}\n\n")
					io.WriteString(w, "event: message_delta\ndata: {\"type\":\"message_delta\",\"usage\":{\"output_tokens\":2}}\n\n")
					if !truncated {
						io.WriteString(w, "event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n")
					}
				} else {
					var request map[string]any
					json.NewDecoder(r.Body).Decode(&request)
					if request["store"] != false || request["stream"] != true {
						t.Error("native OAuth normalization missing")
					}
					if !truncated {
						io.WriteString(w, "event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp-test\",\"usage\":{\"input_tokens\":10,\"output_tokens\":2,\"input_tokens_details\":{\"cached_tokens\":4}}}}\n\n")
					} else {
						io.WriteString(w, "event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\"partial\"}\n\n")
					}
				}
			}))
			defer upstream.Close()
			g := setupGateway(t, provider, upstream)
			path := "/v1/messages"
			if provider == "openai" {
				path = "/v1/responses"
			}
			w := g.request(path, `{"model":"model","stream":true,"max_tokens":20,"input":"hello","messages":[{"role":"user","content":"hello"}]}`)
			if w.Code != 200 {
				t.Fatalf("gateway failed: %d %s", w.Code, w.Body.String())
			}
			q, _ := g.s.Quota(context.Background(), g.user)
			want := int64(12)
			if provider == "openai" {
				want = 9
			}
			if q[0].Used != want || q[1].Used != want || q[0].Reserved != 0 {
				t.Fatalf("incorrect billing: %+v", q)
			}
			truncated = true
			w = g.request(path, `{"model":"model","stream":true,"max_tokens":20,"input":"hello","messages":[{"role":"user","content":"hello"}]}`)
			pending, _ := g.s.PendingCharges(context.Background())
			if len(pending) != 0 {
				t.Fatalf("truncated response left pending: %s", w.Body.String())
			}
			q, _ = g.s.Quota(context.Background(), g.user)
			expectedUsed := want
			if provider == "claude" {
				expectedUsed += 12
			}
			if q[0].Used != expectedUsed || q[0].Reserved != 0 {
				t.Fatal("truncated response did not settle observed usage or release reservation")
			}
			logs, _ := g.s.Logs(context.Background(), g.user, false, 0, "", 0)
			if len(logs) != 2 {
				t.Fatal("missing request log")
			}
			for _, log := range logs {
				raw, _ := json.Marshal(log)
				if bytes.Contains(raw, []byte("upstream-private-token")) || bytes.Contains(raw, []byte("hello")) {
					t.Fatal("sensitive content logged")
				}
			}
		})
	}
}

func TestAntigravityMessagesStreamingAndNonStreaming(t *testing.T) {
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var envelope map[string]any
		if json.NewDecoder(r.Body).Decode(&envelope) != nil || envelope["project"] != "project" || envelope["model"] != "model" {
			t.Error("invalid AG envelope")
		}
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, "data: {\"response\":{\"candidates\":[{\"content\":{\"role\":\"model\",\"parts\":[{\"text\":\"hello\"}]}}],\"usageMetadata\":{\"promptTokenCount\":12,\"candidatesTokenCount\":1,\"cachedContentTokenCount\":2}}}\n\n")
		io.WriteString(w, "data: {\"response\":{\"candidates\":[{\"finishReason\":\"STOP\"}],\"usageMetadata\":{\"promptTokenCount\":12,\"candidatesTokenCount\":3,\"cachedContentTokenCount\":2}}}\n\n")
	}))
	defer upstream.Close()
	g := setupGateway(t, "antigravity", upstream)
	for _, stream := range []bool{true, false} {
		body, _ := json.Marshal(map[string]any{"model": "model", "max_tokens": 20, "stream": stream, "messages": []any{map[string]string{"role": "user", "content": "test"}}})
		w := g.request("/v1/messages", string(body))
		if w.Code != 200 {
			t.Fatalf("AG failed: %d %s", w.Code, w.Body.String())
		}
		if stream && !strings.Contains(w.Body.String(), "message_stop") {
			t.Fatal("missing converted completion")
		}
		if !stream {
			var response struct {
				Content []map[string]any `json:"content"`
			}
			if json.Unmarshal(w.Body.Bytes(), &response) != nil || len(response.Content) == 0 || response.Content[0]["text"] != "hello" {
				t.Fatalf("invalid converted body: %s", w.Body.String())
			}
		}
	}
	q, _ := g.s.Quota(context.Background(), g.user)
	if q[0].Used != 28 {
		t.Fatalf("cached token normalization failed: %+v", q)
	}
}

func TestGatewayRejectsRevokedKeyAndMissingPrice(t *testing.T) {
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { t.Error("unaccepted request reached upstream") }))
	defer upstream.Close()
	g := setupGateway(t, "claude", upstream)
	g.s.DB.Exec("DELETE FROM model_prices")
	if w := g.request("/v1/messages", `{"model":"model"}`); w.Code != 400 {
		t.Fatal("unpriced model accepted")
	}
	g.s.DB.Exec("UPDATE api_keys SET enabled=0")
	if w := g.request("/v1/messages", `{"model":"model"}`); w.Code != 401 {
		t.Fatal("revoked key accepted")
	}
}

func TestCodexModelManifestPreservesMetadata(t *testing.T) {
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/models" || r.URL.Query().Get("client_version") != "0.146.0" {
			t.Error("wrong manifest endpoint")
		}
		io.WriteString(w, `{"models":[{"slug":"model","shell_type":"shell_command","context_window":123456,"supported_reasoning_levels":[{"effort":"low"}]},{"slug":"unpriced","context_window":999}]}`)
	}))
	defer upstream.Close()
	g := setupGateway(t, "openai", upstream)
	r := httptest.NewRequest("GET", "/v1/models?client_version=0.146.0", nil)
	r.Header.Set("Authorization", "Bearer "+g.token)
	w := httptest.NewRecorder()
	g.h.Models(w, r)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "123456") || strings.Contains(w.Body.String(), "unpriced") {
		t.Fatalf("manifest metadata/filter failure: %d %s", w.Code, w.Body.String())
	}
}
