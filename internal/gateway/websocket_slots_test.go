package gateway

import (
	"context"
	"fmt"
	ws "github.com/coder/websocket"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestWebsocketSlotsProtectWorkAndRetireIdle(t *testing.T) {
	h := &Handler{websocketUserLimit: 4}
	now := time.Now()
	slots := make([]*websocketLease, 4)
	contexts := make([]context.Context, 4)
	for i := range slots {
		ctx, cancel := context.WithCancel(context.Background())
		contexts[i] = ctx
		slots[i] = h.acquireTestWebsocketLease(1, string(rune('a'+i)), cancel, now)
		defer slots[i].release()
	}
	if h.acquireTestWebsocketLease(1, "new", func() {}, now) != nil {
		t.Fatal("fresh sockets evicted")
	}
	for _, slot := range slots {
		if !slot.begin(now) {
			t.Fatal("begin failed")
		}
	}
	if h.acquireTestWebsocketLease(1, "new", func() {}, now.Add(10*time.Minute)) != nil {
		t.Fatal("queued/active work evicted")
	}
	if slots[0].expire(now.Add(10 * time.Minute)) {
		t.Fatal("busy socket expired")
	}
	slots[0].finish(now)
	replacement := h.acquireTestWebsocketLease(1, "replacement", func() {}, now.Add(time.Minute))
	if replacement == nil || contexts[0].Err() == nil {
		t.Fatal("idle socket not replaced")
	}
	defer replacement.release()
	if slots[0].begin(now) {
		t.Fatal("retired socket accepted work")
	}
	// Late cleanup from the evicted connection must not remove the new slot.
	slots[0].release()
	if len(h.wsSlots[1]) != 4 {
		t.Fatal("late cleanup changed slot count")
	}
	if replacement.expire(now.Add(time.Minute + time.Second)) {
		t.Fatal("fresh idle socket expired")
	}
	if !replacement.expire(now.Add(7 * time.Minute)) {
		t.Fatal("idle timeout did not release")
	}
	for _, slot := range slots[1:] {
		slot.finish(now)
		slot.release()
	}
	if len(h.wsSlots) != 0 {
		t.Fatal("empty user registry leaked")
	}
}

func TestQuotaRecoveryCanReplaceIdleSocketsWithoutRestart(t *testing.T) {
	var calls atomic.Int64
	g, server := websocketGateway(t, func(w http.ResponseWriter, r *http.Request) {
		conn, err := ws.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer conn.CloseNow()
		for {
			if _, _, err = conn.Read(r.Context()); err != nil {
				return
			}
			calls.Add(1)
			_ = conn.Write(r.Context(), ws.MessageText, []byte(`{"type":"response.completed","response":{"id":"resp_recovered","usage":{"input_tokens":1,"output_tokens":1}}}`))
		}
	})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := g.s.SetLimits(ctx, g.user, g.user, 0, 0); err != nil {
		t.Fatal(err)
	}
	g.h.websocketUserLimit = 4
	var conns []*ws.Conn
	for i := 0; i < 4; i++ {
		conn := dialGatewayWS(t, g, server)
		conns = append(conns, conn)
		sendWS(t, conn, `{"type":"response.create","model":"model","input":"hello"}`)
		event := readWSTerminal(t, conn)
		if string(event["type"]) != `"error"` {
			t.Fatal("quota request was not refused")
		}
	}
	headers := http.Header{"Authorization": []string{"Bearer " + g.token}}
	conn, response, err := ws.Dial(ctx, "ws"+strings.TrimPrefix(server.URL, "http"), &ws.DialOptions{HTTPHeader: headers})
	if err == nil {
		conn.CloseNow()
		t.Fatal("fresh connections exceeded cap")
	}
	if response == nil || response.StatusCode != 429 {
		t.Fatal("wrong cap rejection")
	}
	for {
		logs, err := g.s.LogsPage(ctx, g.user, false, 0, "", 429, "", 0, response.Header.Get("X-Request-ID"))
		if err != nil {
			t.Fatal(err)
		}
		if len(logs) == 1 {
			entry := logs[0]
			d := entry.Diagnostics
			if entry.Amount == nil || *entry.Amount != 0 || d == nil || d.Dispatched ||
				d.Code != "websocket_connection_limit_reached" || d.ConnectionLimit == nil ||
				d.ConnectionLimit.Used != 4 || d.ConnectionLimit.Limit != 4 {
				t.Fatalf("incorrect connection limit diagnostic: %+v", d)
			}
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal("missing connection limit diagnostic")
		case <-time.After(10 * time.Millisecond):
		}
	}
	// Advance lease idle ages without a wall-clock wait, once quota refusals finish.
	for {
		g.h.wsSlotsMu.Lock()
		ready := len(g.h.wsSlots[g.user]) == 4
		for _, slot := range g.h.wsSlots[g.user] {
			if slot.pending != 0 {
				ready = false
			}
		}
		if ready {
			for _, slot := range g.h.wsSlots[g.user] {
				slot.last = time.Now().Add(-time.Minute)
			}
		}
		g.h.wsSlotsMu.Unlock()
		if ready {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal("quota refusals leaked pending work")
		case <-time.After(time.Millisecond):
		}
	}
	if err := g.s.SetLimits(ctx, g.user, g.user, 10000000, 100000000); err != nil {
		t.Fatal(err)
	}
	replacement := dialGatewayWS(t, g, server)
	conns = append(conns, replacement)
	sendWS(t, replacement, `{"type":"response.create","model":"model","input":"hello"}`)
	event := readWSTerminal(t, replacement)
	if string(event["type"]) != `"response.completed"` || calls.Load() != 1 {
		t.Fatal("quota recovery failed", event, calls.Load())
	}
	for _, conn := range conns {
		conn.CloseNow()
	}
	for {
		g.h.wsSlotsMu.Lock()
		left := len(g.h.wsSlots)
		g.h.wsSlotsMu.Unlock()
		_, total := g.h.websocketCounts(g.user)
		if left == 0 && total == 0 && g.h.wsQueueBytes.Load() == 0 {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal("connection resources not released")
		case <-time.After(time.Millisecond):
		}
	}
}

func (h *Handler) acquireTestWebsocketLease(user int64, id string, cancel context.CancelFunc, now time.Time) *websocketLease {
	lease, _, _ := h.reserveWebsocketLease(user, id, cancel, now)
	if lease != nil && !lease.activate(now) {
		lease.release()
		return nil
	}
	return lease
}

func TestConfiguredWebsocketUserLimit(t *testing.T) {
	if got := (&Handler{}).websocketConnectionLimit(); got != 32 {
		t.Fatalf("default connection limit = %d, want 32", got)
	}
	for _, limit := range []int{1, 6, 32} {
		h := &Handler{websocketUserLimit: limit}
		now := time.Now()
		for i := 0; i < limit; i++ {
			lease, status, _ := h.reserveWebsocketLease(1, fmt.Sprint(i), func() {}, now)
			if lease == nil || status != 0 || !lease.activate(now) {
				t.Fatalf("limit %d: connection %d rejected, status %d", limit, i, status)
			}
		}
		if lease, status, _ := h.reserveWebsocketLease(1, "overflow", func() {}, now); lease != nil || status != 429 {
			t.Fatalf("limit %d: overflow accepted or wrong status %d", limit, status)
		}
		if lease, status, _ := h.reserveWebsocketLease(2, "other-user", func() {}, now); lease == nil || status != 0 {
			t.Fatalf("limit %d: other user rejected, status %d", limit, status)
		}
		replacement, status, _ := h.reserveWebsocketLease(1, "replacement", func() {}, now.Add(time.Minute))
		if replacement == nil || status != 0 || !replacement.activate(now.Add(time.Minute)) {
			t.Fatalf("limit %d: idle replacement rejected, status %d", limit, status)
		}
		if users, _ := h.websocketCounts(1); users != limit {
			t.Fatalf("limit %d: replacement changed count to %d", limit, users)
		}
	}
}
