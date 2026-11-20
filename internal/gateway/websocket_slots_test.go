package gateway

import (
	"context"
	ws "github.com/coder/websocket"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestWebsocketSlotsProtectWorkAndRetireIdle(t *testing.T) {
	h := &Handler{}
	now := time.Now()
	slots := make([]*websocketLease, 4)
	contexts := make([]context.Context, 4)
	for i := range slots {
		ctx, cancel := context.WithCancel(context.Background())
		contexts[i] = ctx
		slots[i] = h.acquireWebsocketLease(1, string(rune('a'+i)), cancel, now)
		defer slots[i].release()
	}
	if h.acquireWebsocketLease(1, "new", func() {}, now) != nil {
		t.Fatal("fresh sockets evicted")
	}
	for _, slot := range slots {
		if !slot.begin(now) {
			t.Fatal("begin failed")
		}
	}
	if h.acquireWebsocketLease(1, "new", func() {}, now.Add(10*time.Minute)) != nil {
		t.Fatal("queued/active work evicted")
	}
	if slots[0].expire(now.Add(10 * time.Minute)) {
		t.Fatal("busy socket expired")
	}
	slots[0].finish(now)
	replacement := h.acquireWebsocketLease(1, "replacement", func() {}, now.Add(time.Minute))
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
		if left == 0 && g.h.wsTotal.Load() == 0 && g.h.wsQueueBytes.Load() == 0 {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal("connection resources not released")
		case <-time.After(time.Millisecond):
		}
	}
}
