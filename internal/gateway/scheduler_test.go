package gateway

import (
	"context"
	"encoding/json"
	"fiftyapi/internal/accounts"
	"fiftyapi/internal/modelref"
	"fiftyapi/internal/providers"
	"fiftyapi/internal/store"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestMixedOAuthAndGatewayPoolPriorityWeightAndStickyRoute(t *testing.T) {
	var nativeCalls, relayCalls atomic.Int64
	native := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		nativeCalls.Add(1)
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body["model"] != "model" || r.Header.Get("Authorization") != "Bearer upstream-private-token" {
			t.Error("native route mapping or credential incorrect")
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, schedulerSuccess)
	}))
	defer native.Close()
	relay := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		relayCalls.Add(1)
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		if r.URL.Path != "/v1/responses" || body["model"] != "remote-sol" || r.Header.Get("Authorization") != "Bearer relay-key" {
			t.Error("gateway route mapping or credential incorrect")
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, schedulerSuccess)
	}))
	defer relay.Close()

	g := setupGateway(t, "openai", native)
	relayConfig := providers.Config{Provider: "gateway", Gateway: &providers.GatewayConfig{Platform: "sub2api", BaseURL: relay.URL, Protocols: []string{"responses"}, Discovery: "manual", AuthMode: "bearer", Models: []providers.GatewayModel{{ID: "gpt-6.1-sol", UpstreamID: "remote-sol", Protocols: []string{"responses"}}}}}
	relayID, err := g.h.accounts.Import(context.Background(), g.user, "sub2api", accounts.Credential{Config: relayConfig, APIKey: "relay-key"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = g.s.SyncGatewayModels(context.Background(), relayID, []modelref.Mapping{{ID: "model", UpstreamID: "remote-sol", Protocols: []string{"responses"}}}); err != nil {
		t.Fatal(err)
	}
	_ = g.s.AccountState(context.Background(), relayID, "catalog_ready", "")
	g.h.client = func(config providers.Config) *http.Client {
		if config.Provider == "gateway" {
			return relay.Client()
		}
		return native.Client()
	}

	nativePolicy, relayPolicy := store.DefaultAccountPolicy(), store.DefaultAccountPolicy()
	nativePolicy.Priority = 50
	relayPolicy.Priority = 10
	_ = g.s.SetAccountPolicy(context.Background(), g.user, g.account, nativePolicy)
	_ = g.s.SetAccountPolicy(context.Background(), g.user, relayID, relayPolicy)
	if out := poolRequest(g, "sticky-mixed", `{"model":"model","input":"first"}`); out.Code != 200 {
		t.Fatalf("relay request failed: %d %s", out.Code, out.Body.String())
	}

	// Changing priority affects new sessions but must not move a healthy bound
	// conversation away from its original source.
	nativePolicy.Priority = 0
	relayPolicy.Priority = 80
	_ = g.s.SetAccountPolicy(context.Background(), g.user, g.account, nativePolicy)
	_ = g.s.SetAccountPolicy(context.Background(), g.user, relayID, relayPolicy)
	if out := poolRequest(g, "sticky-mixed", `{"model":"model","input":"second"}`); out.Code != 200 {
		t.Fatalf("sticky request failed: %d %s", out.Code, out.Body.String())
	}
	if out := poolRequest(g, "fresh-mixed", `{"model":"model","input":"fresh"}`); out.Code != 200 {
		t.Fatalf("new session request failed: %d %s", out.Code, out.Body.String())
	}
	if relayCalls.Load() != 2 || nativeCalls.Load() != 1 {
		t.Fatalf("priority/sticky routing mismatch: relay=%d native=%d", relayCalls.Load(), nativeCalls.Load())
	}
}

const schedulerSuccess = "event: response.created\ndata: {\"type\":\"response.created\",\"response\":{\"id\":\"good-response\"}}\n\nevent: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"id\":\"good-response\",\"status\":\"completed\",\"output\":[{\"type\":\"message\",\"content\":[{\"type\":\"output_text\",\"text\":\"OK\"}]}],\"usage\":{\"input_tokens\":10,\"output_tokens\":2,\"input_tokens_details\":{\"cached_tokens\":4}}}}\n\n"

func addPoolAccount(t *testing.T, g testGateway) int64 {
	t.Helper()
	ctx := context.Background()
	c, e := g.h.accounts.Access(ctx, g.account)
	if e != nil {
		t.Fatal(e)
	}
	c.Token.AccessToken = "second-fixture-token"
	id, e := g.h.accounts.Import(ctx, g.user, "second", c)
	if e != nil {
		t.Fatal(e)
	}
	if e = g.s.SyncModels(ctx, id, "openai", []string{"model"}); e != nil {
		t.Fatal(e)
	}
	if e = g.s.AccountState(ctx, id, "catalog_ready", ""); e != nil {
		t.Fatal(e)
	}
	p := store.DefaultAccountPolicy()
	p.Priority = 0
	p.WaitMS = 0
	if e = g.s.SetAccountPolicy(ctx, g.user, g.account, p); e != nil {
		t.Fatal(e)
	}
	p.Priority = 50
	if e = g.s.SetAccountPolicy(ctx, g.user, id, p); e != nil {
		t.Fatal(e)
	}
	return id
}
func poolRequest(g testGateway, session, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest("POST", "/v1/responses", strings.NewReader(body))
	r.Header.Set("Authorization", "Bearer "+g.token)
	r.Header.Set("session_id", session)
	w := httptest.NewRecorder()
	g.h.Serve(w, r)
	return w
}
func TestPoolFailoverAndPersistentCooldown(t *testing.T) {
	for _, mode := range []string{"429", "stream-limit", "model-denied", "unknown-500", "partial-output"} {
		t.Run(mode, func(t *testing.T) {
			var mu sync.Mutex
			calls := []string{}
			upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				first := r.Header.Get("Authorization") == "Bearer upstream-private-token"
				mu.Lock()
				if first {
					calls = append(calls, "first")
				} else {
					calls = append(calls, "second")
				}
				mu.Unlock()
				if first {
					switch mode {
					case "429":
						w.Header().Set("Retry-After", "20")
						w.Header().Set("x-codex-primary-used-percent", "100")
						w.Header().Set("x-codex-primary-reset-after-seconds", "30")
						w.WriteHeader(429)
						io.WriteString(w, `{"error":{"code":"rate_limit_exceeded"}}`)
						return
					case "model-denied":
						w.WriteHeader(400)
						io.WriteString(w, `{"error":{"code":"model_not_found"}}`)
						return
					case "unknown-500":
						w.WriteHeader(500)
						io.WriteString(w, `{"error":{"message":"unknown execution state"}}`)
						return
					case "partial-output":
						w.Header().Set("Content-Type", "text/event-stream")
						io.WriteString(w, "event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\"PARTIAL\"}\n\nevent: response.failed\ndata: {\"type\":\"response.failed\",\"response\":{\"error\":{\"code\":\"server_is_overloaded\"}}}\n\n")
						return
					case "stream-limit":
						w.Header().Set("Content-Type", "text/event-stream")
						io.WriteString(w, "event: response.created\ndata: {\"type\":\"response.created\",\"response\":{\"id\":\"discarded-response\"}}\n\nevent: response.failed\ndata: {\"type\":\"response.failed\",\"response\":{\"error\":{\"code\":\"usage_limit_reached\"}}}\n\n")
						return
					}
				}
				w.Header().Set("Content-Type", "text/event-stream")
				io.WriteString(w, schedulerSuccess)
			}))
			defer upstream.Close()
			g := setupGateway(t, "openai", upstream)
			second := addPoolAccount(t, g)
			response := poolRequest(g, "pool-session", `{"model":"model","stream":true,"input":"hello"}`)
			logs, e := g.s.Logs(context.Background(), g.user, false, 0, "", 0)
			if e != nil || len(logs) != 1 {
				t.Fatal("missing single logical request log")
			}
			attempts, e := g.s.RequestAttempts(context.Background(), g.user, true, logs[0].ID)
			if e != nil {
				t.Fatal(e)
			}
			if mode == "unknown-500" || mode == "partial-output" {
				if len(calls) != 1 || len(attempts) != 1 {
					t.Fatal("unsafe request was replayed")
				}
				if mode == "partial-output" && !strings.Contains(response.Body.String(), "PARTIAL") {
					t.Fatal("partial output lost")
				}
				return
			}
			if response.Code != 200 || len(calls) != 2 || len(attempts) != 2 || *logs[0].AccountID != second || logs[0].Amount == nil || *logs[0].Amount != 9 {
				t.Fatalf("failover/settlement incorrect: HTTP=%d calls=%d attempts=%d", response.Code, len(calls), len(attempts))
			}
			if strings.Contains(response.Body.String(), "discarded-response") {
				t.Fatal("failed-attempt protocol identifiers leaked")
			}
			q, e := g.s.Quota(context.Background(), g.user)
			if e != nil || q[0].Used != 9 || q[1].Used != 9 || q[0].Reserved != 0 {
				t.Fatal("logical request billed more than once")
			}
			bound, e := g.s.GatewaySession(context.Background(), sessionHash(httptest.NewRequest("POST", "/", nil), g.user, "model\x00responses", map[string]json.RawMessage{"input": json.RawMessage(`"hello"`)}))
			_ = bound
			_ = e
			health, e := g.s.AccountHealth(context.Background(), g.account)
			if e != nil || len(health.Cooldowns) == 0 {
				t.Fatal("cooldown not persisted")
			}
			if mode == "429" && (health.PrimaryUsed == nil || *health.PrimaryUsed != 100 || health.PrimaryReset == nil) {
				t.Fatal("upstream quota snapshot lost")
			}
			// A new scheduler has empty in-memory state but must honor durable cooldown.
			replacement := New(g.s, g.h.accounts)
			replacement.client = g.h.client
			g.h = replacement
			again := poolRequest(g, "new-session", `{"model":"model","input":"next"}`)
			if again.Code != 200 || calls[len(calls)-1] != "second" {
				t.Fatal("restart lost account cooldown")
			}
		})
	}
}
func TestPortableBindingMovesAndOpaqueBindingStays(t *testing.T) {
	for _, opaque := range []bool{false, true} {
		t.Run(fmt.Sprint(opaque), func(t *testing.T) {
			var reject atomic.Bool
			var secondCalls atomic.Int64
			upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Authorization") == "Bearer second-fixture-token" {
					secondCalls.Add(1)
				} else if reject.Load() {
					w.WriteHeader(429)
					io.WriteString(w, `{"error":{"code":"usage_limit_reached"}}`)
					return
				}
				w.Header().Set("Content-Type", "text/event-stream")
				io.WriteString(w, schedulerSuccess)
			}))
			defer upstream.Close()
			g := setupGateway(t, "openai", upstream)
			second := addPoolAccount(t, g)
			body := `{"model":"model","input":[{"role":"user","content":"hello"}]}`
			if first := poolRequest(g, "stable-session", body); first.Code != 200 {
				t.Fatal("initial request failed")
			}
			reject.Store(true)
			if opaque {
				body = `{"model":"model","input":[{"role":"user","content":"hello"},{"type":"reasoning","encrypted_content":"opaque-fixture"}]}`
			}
			out := poolRequest(g, "stable-session", body)
			req := httptest.NewRequest("POST", "/", nil)
			req.Header.Set("session_id", "stable-session")
			var parsed map[string]json.RawMessage
			json.Unmarshal([]byte(body), &parsed)
			bound, e := g.s.GatewaySession(context.Background(), sessionHash(req, g.user, "model\x00responses", parsed))
			if e != nil {
				t.Fatal(e)
			}
			if opaque {
				if out.Code != 429 || secondCalls.Load() != 0 || bound != g.account {
					t.Fatal("account-bound context was moved")
				}
			} else if out.Code != 200 || secondCalls.Load() != 1 || bound != second {
				t.Fatal("portable context did not move to healthy account")
			}
		})
	}
}
func TestWeightedSelectionAndCanceledQueue(t *testing.T) {
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { t.Error("selection-only test must not dispatch") }))
	defer upstream.Close()
	g := setupGateway(t, "openai", upstream)
	second := addPoolAccount(t, g)
	// A large weight must never jump across a priority boundary.
	lower := store.DefaultAccountPolicy()
	lower.Priority = 50
	lower.Weight = 100
	if e := g.s.SetAccountPolicy(context.Background(), g.user, second, lower); e != nil {
		t.Fatal(e)
	}
	for i := 0; i < 30; i++ {
		selected, e := g.h.selectAccount(context.Background(), g.user, "model", "model", nil, true, map[int64]bool{}, "priority-"+fmt.Sprint(i))
		if e != nil {
			t.Fatal(e)
		}
		if selected.ID != g.account {
			t.Fatal("weight crossed a strict priority layer")
		}
		selected.Release()
	}
	p := store.DefaultAccountPolicy()
	p.Concurrency = 2 // This test saturates two slots independently of the default.
	p.WaitMS = 100
	p.MaxQueue = 1
	p.Priority = 50
	if e := g.s.SetAccountPolicy(context.Background(), g.user, g.account, p); e != nil {
		t.Fatal(e)
	}
	p.Weight = 4
	if e := g.s.SetAccountPolicy(context.Background(), g.user, second, p); e != nil {
		t.Fatal(e)
	}
	count := 0
	for i := 0; i < 200; i++ {
		selected, e := g.h.selectAccount(context.Background(), g.user, "model", "model", nil, true, map[int64]bool{}, fmt.Sprint(i))
		if e != nil {
			t.Fatal(e)
		}
		if selected.ID == second {
			count++
		}
		selected.Release()
	}
	if count < 130 || count > 190 {
		t.Fatalf("weighted selection skewed: %d/200", count)
	}
	// Saturate both accounts. A canceled waiter must not leak a slot or queue entry.
	for _, id := range []int64{g.account, second} {
		if !g.h.slot(id).acquire(2) || !g.h.slot(id).acquire(2) {
			t.Fatal("cannot saturate slots")
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	if selected, e := g.h.selectAccount(ctx, g.user, "model", "model", nil, true, map[int64]bool{}, "queued"); e == nil {
		selected.Release()
		t.Fatal("canceled queue acquired slot")
	}
	for _, id := range []int64{g.account, second} {
		active, waiting := g.h.slot(id).load()
		if active != 2 || waiting != 0 {
			t.Fatal("queue leaked resources")
		}
		g.h.slot(id).release()
		g.h.slot(id).release()
	}
}

func TestServerSideToolSignalsForbidReplay(t *testing.T) {
	for _, kind := range []string{"function_call", "mcp_call", "web_search_call", "computer_call", "code_interpreter_call", "shell_call", "image_generation_call"} {
		t.Run(kind, func(t *testing.T) {
			w := httptest.NewRecorder()
			buffer := newAttemptWriter(w)
			buffer.Header().Set("Content-Type", "text/event-stream")
			event := fmt.Sprintf(`{"type":"response.output_item.added","item":{"type":%q,"id":"fixture"}}`, kind)
			if e := emitSSE(buffer, "response.output_item.added", []byte(event)); e != nil {
				t.Fatal(e)
			}
			if !buffer.committed {
				t.Fatal("server-side execution may be replayed")
			}
		})
	}
	if historyPortable(map[string]json.RawMessage{"messages": json.RawMessage(`[{"role":"assistant","content":[{"type":"redacted_thinking","data":"opaque"}]}]`)}) {
		t.Fatal("redacted account-bound context considered portable")
	}
}

func TestInitialContentForbidsReplay(t *testing.T) {
	for _, frame := range []string{`{"type":"response.content_part.added","part":{"type":"output_text","text":"already emitted"}}`, `{"type":"content_block_start","content_block":{"type":"thinking","thinking":"already emitted"}}`} {
		if !semanticFrame([]byte("data: " + frame + "\n\n")) {
			t.Fatal("initial content was incorrectly treated as replayable metadata")
		}
	}
}
