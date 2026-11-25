package gateway

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"fiftyapi/internal/backup"
	"fiftyapi/internal/store"
	ws "github.com/coder/websocket"
)

func TestMixedProductionLoad(t *testing.T) {
	if os.Getenv("FIFTYAPI_MIXED_LOAD") != "1" {
		t.Skip("set FIFTYAPI_MIXED_LOAD=1")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	const delta = `{"type":"response.output_text.delta","delta":"fixture"}`
	const done = `{"type":"response.completed","response":{"id":"resp_mixed","usage":{"input_tokens":10,"output_tokens":2}}}`
	var streamed atomic.Int64
	g, socketServer := websocketGateway(t, func(w http.ResponseWriter, r *http.Request) {
		emit := func(event string) error {
			_, err := fmt.Fprintf(w, "data: %s\n\n", event)
			w.(http.Flusher).Flush()
			return err
		}
		if r.Header.Get("Upgrade") == "websocket" {
			c, err := ws.Accept(w, r, nil)
			if err != nil {
				return
			}
			defer c.CloseNow()
			if _, _, err = c.Read(r.Context()); err != nil {
				return
			}
			emit = func(event string) error { return c.Write(r.Context(), ws.MessageText, []byte(event)) }
		} else {
			w.Header().Set("Content-Type", "text/event-stream")
		}
		for i := 0; i < 20; i++ {
			if emit(delta) != nil {
				return
			}
			if i == 0 {
				streamed.Add(1)
			}
			time.Sleep(100 * time.Millisecond)
		}
		_ = emit(done)
	})
	policy, err := g.s.AccountPolicy(ctx, g.account)
	if err != nil {
		t.Fatal(err)
	}
	policy.Concurrency = 16
	if err = g.s.SetAccountPolicy(ctx, g.user, g.account, policy); err != nil {
		t.Fatal(err)
	}
	if _, err = g.s.DB.ExecContext(ctx, `WITH RECURSIVE seed(i) AS (SELECT 1 UNION ALL SELECT i+1 FROM seed WHERE i<100000)
 INSERT INTO request_logs(id,user_id,model,provider,status,input_tokens,output_tokens,amount,duration_ms,created_at,diagnostics)
 SELECT 'history-'||i,?,'model','openai',CASE WHEN i%10=0 THEN 502 ELSE 200 END,10,2,12,2000,unixepoch()-i,'{"protocol":"responses","code":"stream_interrupted"}' FROM seed`, g.user); err != nil {
		t.Fatal(err)
	}
	sse := httptest.NewServer(http.HandlerFunc(g.h.Serve))
	defer sse.Close()
	object := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "PUT" {
			w.WriteHeader(405)
			return
		}
		_, _ = io.Copy(io.Discard, r.Body)
	}))
	defer object.Close()
	svc := backup.New(g.s, make([]byte, 32))
	defer svc.Close(context.Background())
	cfg := backup.DefaultConfig()
	cfg.Endpoint, cfg.Bucket, cfg.AccessKey, cfg.SecretKey = object.URL, "fixture-bucket", "fixture", "fixture"
	if err = svc.SaveConfig(ctx, g.user, cfg); err != nil {
		t.Fatal(err)
	}
	type sample struct {
		first, total time.Duration
		transport    string
	}
	results := make(chan sample, 8)
	faults := make(chan error, 8)
	base := g.s.DB.Stats()
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(socket bool) {
			defer wg.Done()
			start := time.Now()
			var first time.Duration
			var complete bool
			transport := "SSE"
			auth := http.Header{"Authorization": []string{"Bearer " + g.token}}
			if socket {
				transport = "WebSocket"
				c, _, err := ws.Dial(ctx, "ws"+strings.TrimPrefix(socketServer.URL, "http"), &ws.DialOptions{HTTPHeader: auth})
				if err != nil {
					faults <- err
					return
				}
				defer c.CloseNow()
				if err = c.Write(ctx, ws.MessageText, []byte(`{"type":"response.create","model":"model","input":"fixture"}`)); err != nil {
					faults <- err
					return
				}
				for !complete {
					_, raw, err := c.Read(ctx)
					if err != nil {
						faults <- err
						return
					}
					if first == 0 && strings.Contains(string(raw), "response.output_text.delta") {
						first = time.Since(start)
					}
					complete = strings.Contains(string(raw), "response.completed")
					if strings.Contains(string(raw), `"type":"error"`) {
						faults <- fmt.Errorf("websocket fixture failed: %s", raw)
						return
					}
				}
			} else {
				req, err := http.NewRequestWithContext(ctx, "POST", sse.URL+"/v1/responses", strings.NewReader(`{"model":"model","stream":true,"input":"fixture"}`))
				if err != nil {
					faults <- err
					return
				}
				req.Header = auth
				resp, err := sse.Client().Do(req)
				if err != nil {
					faults <- err
					return
				}
				defer resp.Body.Close()
				scan := bufio.NewScanner(resp.Body)
				for scan.Scan() {
					line := scan.Text()
					if first == 0 && strings.Contains(line, "response.output_text.delta") {
						first = time.Since(start)
					}
					complete = complete || strings.Contains(line, "response.completed")
				}
				if resp.StatusCode != 200 || scan.Err() != nil || !complete {
					faults <- fmt.Errorf("SSE status %d, completed %t: %v", resp.StatusCode, complete, scan.Err())
					return
				}
			}
			if first == 0 {
				faults <- fmt.Errorf("%s missed first output", transport)
				return
			}
			results <- sample{first, time.Since(start), transport}
		}(i%2 == 0)
	}
	wait := func(ready func() bool) {
		t.Helper()
		for !ready() {
			select {
			case err := <-faults:
				t.Fatal(err)
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			case <-time.After(10 * time.Millisecond):
			}
		}
	}
	wait(func() bool { return streamed.Load() == 8 })
	rec, err := svc.Start(g.user, "manual", "")
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 4; i++ {
		dashboard, err := g.s.Dashboard(ctx, g.user, true, 24, 3600, 0)
		if err != nil {
			t.Fatal(err)
		}
		if len(dashboard.Failures) != 1 || dashboard.Failures[0].Code != "stream_interrupted" {
			t.Fatalf("unexpected failure groups: %+v", dashboard.Failures)
		}
		group := dashboard.Failures[0]
		filter := store.LogFilter{Admin: true, FailureCode: &group.Code, Protocol: &group.Protocol, AccountID: &group.AccountID, Since: dashboard.FailureSince}
		page, err := g.s.LogListPage(ctx, filter, 1, 20)
		if err != nil || int64(page.Total) != group.Requests {
			t.Fatalf("failure drilldown mismatch: %d != %d (%v)", page.Total, group.Requests, err)
		}
	}
	wait(func() bool {
		r, e := svc.Record(ctx, rec.ID)
		if e != nil || r.Status == "failed" {
			t.Fatalf("backup failed: %s %v", r.Error, e)
		}
		return r.Status == "completed"
	})
	wg.Wait()
	for len(faults) > 0 {
		t.Error(<-faults)
	}
	if t.Failed() {
		return
	}
	close(results)
	wait(func() bool {
		windows, e := g.s.Quota(ctx, g.user)
		if e != nil {
			t.Fatal(e)
		}
		return len(windows) == 2 && windows[0].Reserved == 0 && windows[1].Reserved == 0 && windows[0].Used == 96 && windows[1].Used == 96
	})
	var all []sample
	for s := range results {
		all = append(all, s)
	}
	p95 := func(samples []time.Duration) time.Duration {
		sort.Slice(samples, func(i, j int) bool { return samples[i] < samples[j] })
		return samples[(len(samples)*95+99)/100-1].Round(time.Millisecond)
	}
	for _, transport := range []string{"SSE", "WebSocket"} {
		var first, total []time.Duration
		for _, s := range all {
			if s.transport == transport {
				first = append(first, s.first)
				total = append(total, s.total)
			}
		}
		if len(first) != 4 {
			t.Fatalf("unexpected sample count for %s: %d", transport, len(first))
		}
		t.Logf("%s first output p95=%s total p95=%s", transport, p95(first), p95(total))
	}
	end := g.s.DB.Stats()
	t.Logf("100000 seeded logs, 8 streams, 4 dashboard/log reads and one online backup; SQLite waits=%d wait time=%s; both windows settled 96 microUSD with no reservations", end.WaitCount-base.WaitCount, (end.WaitDuration - base.WaitDuration).Round(time.Millisecond))
}
