package gateway

import (
	"context"
	"fiftyapi/internal/providers"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

const agSuccess = "data: {\"response\":{\"candidates\":[{\"content\":{\"role\":\"model\",\"parts\":[{\"text\":\"OK\"}]},\"finishReason\":\"STOP\"}],\"usageMetadata\":{\"promptTokenCount\":12,\"candidatesTokenCount\":3}}}\n\n"
const agRequest = `{"model":"model","stream":true,"max_tokens":32,"messages":[{"role":"user","content":"test"}]}`

func TestAntigravityFailuresAndCooldown(t *testing.T) {
	for _, scenario := range []string{"truncated", "invalid_json", "missing_usage", "timeout", "429"} {
		t.Run(scenario, func(t *testing.T) {
			var calls atomic.Int64
			upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				io.Copy(io.Discard, r.Body)
				if scenario == "timeout" {
					select {
					case <-r.Context().Done():
					case <-time.After(2 * time.Second):
					}
					return
				}
				if scenario == "429" {
					w.WriteHeader(429)
					return
				}
				w.Header().Set("Content-Type", "text/event-stream")
				switch scenario {
				case "missing_usage":
					io.WriteString(w, `data: {"response":{"candidates":[{"finishReason":"STOP","content":{"role":"model","parts":[{"text":"ok"}]}}]}}`+"\n\n")
				case "invalid_json":
					io.WriteString(w, "data: {invalid\n\n")
				default:
					io.WriteString(w, `data: {"response":{"candidates":[{"content":{"role":"model","parts":[{"text":"partial"}]}}],"usageMetadata":{"promptTokenCount":12,"candidatesTokenCount":1}}}`+"\n\n")
				}
			}))
			defer upstream.Close()
			g := setupGateway(t, "antigravity", upstream)
			if scenario == "timeout" {
				g.h.client = func(providers.Config) *http.Client {
					c := upstream.Client()
					c.Timeout = 75 * time.Millisecond
					return c
				}
			}
			w := g.request("/v1/messages", agRequest)
			pending, err := g.s.PendingCharges(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			quota, err := g.s.Quota(context.Background(), g.user)
			if err != nil {
				t.Fatal(err)
			}
			if scenario == "429" {
				if w.Code != 429 || len(pending) != 0 || quota[0].Used != 0 || quota[0].Reserved != 0 {
					t.Fatal("429 rejection was charged or held pending")
				}
				if retry := g.request("/v1/messages", agRequest); retry.Code != 503 {
					t.Fatal("cooldown not applied")
				}
			}
			expected := int64(0)
			if scenario == "truncated" {
				expected = 13
			}
			if len(pending) != 0 || quota[0].Used != expected || quota[0].Reserved != 0 {
				t.Fatal("automatic settlement policy violated", quota, pending)
			}

			if calls.Load() != 1 {
				t.Fatal("inference was automatically replayed")
			}
			logs, err := g.s.Logs(context.Background(), g.user, false, 0, "", 0)
			if err != nil || len(logs) == 0 {
				t.Fatal("missing failure log")
			}
			if logs[0].Amount == nil || *logs[0].Amount != expected {
				t.Fatal("automatic settlement missing or incorrect")
			}
		})
	}
}

func TestAntigravityClientCancellationReleasesSlot(t *testing.T) {
	canceled := make(chan struct{})
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.Copy(io.Discard, r.Body)
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, `data: {"response":{"candidates":[{"content":{"role":"model","parts":[{"text":"partial"}]}}]}}`+"\n\n")
		w.(http.Flusher).Flush()
		select {
		case <-r.Context().Done():
		case <-time.After(3 * time.Second):
		}
		close(canceled)
	}))
	defer upstream.Close()
	g := setupGateway(t, "antigravity", upstream)
	server := httptest.NewServer(http.HandlerFunc(g.h.Serve))
	defer server.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	request, _ := http.NewRequestWithContext(ctx, "POST", server.URL+"/v1/messages", strings.NewReader(agRequest))
	request.Header.Set("Authorization", "Bearer "+g.token)
	response, err := server.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	buffer := make([]byte, 64)
	if _, err = response.Body.Read(buffer); err != nil {
		t.Fatal(err)
	}
	cancel()
	response.Body.Close()
	select {
	case <-canceled:
	case <-time.After(3 * time.Second):
		t.Fatal("client cancellation did not reach upstream")
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		logs, e := g.s.Logs(context.Background(), g.user, false, 0, "", 0)
		if e == nil && len(logs) == 1 && logs[0].Amount != nil {
			pending, e := g.s.PendingCharges(context.Background())
			if e != nil || len(pending) != 0 {
				t.Fatal("canceled request left pending")
			}
			slot, _ := g.h.slots.Load(g.account)
			state := slot.(*accountSlot)
			state.mu.Lock()
			active := state.active
			state.mu.Unlock()
			if active == 0 {
				return
			}
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("canceled request did not finish logging and release slot")
}

func TestAntigravityConcurrentGatewayLoad(t *testing.T) {
	var calls, active, peak atomic.Int64
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		n := active.Add(1)
		defer active.Add(-1)
		for old := peak.Load(); n > old; old = peak.Load() {
			if peak.CompareAndSwap(old, n) {
				break
			}
		}
		time.Sleep(5 * time.Millisecond)
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, agSuccess)
	}))
	defer upstream.Close()
	g := setupGateway(t, "antigravity", upstream)
	policy, err := g.s.AccountPolicy(context.Background(), g.account)
	if err != nil {
		t.Fatal(err)
	}
	policy.Concurrency = 8
	if err = g.s.SetAccountPolicy(context.Background(), g.user, g.account, policy); err != nil {
		t.Fatal(err)
	}
	if err = g.s.AccountState(context.Background(), g.account, "catalog_ready", ""); err != nil {
		t.Fatal(err)
	}
	const clients = 50
	users := make([]int64, clients)
	tokens := make([]string, clients)
	for i := 0; i < clients; i++ {
		users[i], err = g.s.CreateUser(context.Background(), g.user, fmt.Sprintf("member-%d", i), "test-hash", 10000000, 100000000)
		if err != nil {
			t.Fatal(err)
		}
		_, tokens[i], err = g.s.CreateAPIKey(context.Background(), users[i], "load-test")
		if err != nil {
			t.Fatal(err)
		}
	}
	var wg sync.WaitGroup
	var success, rejected, unexpected atomic.Int64
	timings := make(chan time.Duration, clients*4)
	start := time.Now()
	for client := 0; client < clients; client++ {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			for turn := 0; turn < 4; turn++ {
				request := httptest.NewRequest("POST", "/v1/messages", strings.NewReader(agRequest))
				request.Header.Set("Authorization", "Bearer "+tokens[index])
				request.Header.Set("session_id", fmt.Sprintf("load-%d", index))
				w := httptest.NewRecorder()
				begin := time.Now()
				g.h.Serve(w, request)
				timings <- time.Since(begin)
				switch w.Code {
				case 200:
					success.Add(1)
				case 503:
					rejected.Add(1)
				default:
					unexpected.Add(1)
				}
			}
		}(client)
	}
	wg.Wait()
	close(timings)
	if unexpected.Load() != 0 || success.Load() == 0 || calls.Load() != success.Load() || peak.Load() > 8 {
		t.Fatalf("bad concurrent result: successes=%d rejected=%d unexpected=%d calls=%d peak=%d", success.Load(), rejected.Load(), unexpected.Load(), calls.Load(), peak.Load())
	}
	var reconciled int64
	for _, user := range users {
		logs, e := g.s.Logs(context.Background(), user, false, 0, "", 0)
		if e != nil || len(logs) != 4 {
			t.Fatal("per-user request isolation failed")
		}
		var amount int64
		for _, log := range logs {
			if log.UserID != user || log.Amount == nil {
				t.Fatal("per-user log leak or unsettled amount")
			}
			amount += *log.Amount
		}
		windows, e := g.s.Quota(context.Background(), user)
		if e != nil {
			t.Fatal(e)
		}
		for _, w := range windows {
			if w.Used != amount || w.Reserved != 0 {
				t.Fatal("concurrent per-user quota drift")
			}
		}
		reconciled += amount
	}
	if reconciled != 15*success.Load() {
		t.Fatal("total and per-user amount mismatch")
	}
	var count int
	if err = g.s.DB.QueryRow("SELECT count(*) FROM request_logs").Scan(&count); err != nil || count != clients*4 {
		t.Fatal("concurrent log count mismatch")
	}
	pending, err := g.s.PendingCharges(context.Background())
	if err != nil || len(pending) != 0 {
		t.Fatal("unexpected pending request under healthy load")
	}
	durations := []time.Duration{}
	for d := range timings {
		durations = append(durations, d)
	}
	sort.Slice(durations, func(i, j int) bool { return durations[i] < durations[j] })
	t.Logf("mock load: clients=%d requests=%d success=%d capacity_503=%d peak_upstream=%d elapsed=%s p95=%s; both quota windows/logs reconciled", clients, count, success.Load(), rejected.Load(), peak.Load(), time.Since(start).Round(time.Millisecond), durations[len(durations)*95/100].Round(time.Millisecond))
}
