package gateway

import (
	"context"
	"fmt"
	ws "github.com/coder/websocket"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestWebsocketAdmissionRollbackAndBusyVictim(t *testing.T) {
	h := &Handler{}
	now := time.Now()
	var original []*websocketLease
	for i := 0; i < 4; i++ {
		original = append(original, h.acquireTestWebsocketLease(1, fmt.Sprint(i), func() {}, now))
	}
	next, status := h.reserveWebsocketLease(1, "failed", func() {}, now.Add(time.Minute))
	if status != 0 || next == nil {
		t.Fatal(status)
	}
	for _, old := range original {
		if old.retired {
			t.Fatal("reservation prematurely evicted old connection")
		}
	}
	if next.begin(now) {
		t.Fatal("handshake reservation accepted work")
	}
	next.release()
	if user, total := h.websocketCounts(1); user != 4 || total != 4 {
		t.Fatal(user, total)
	}
	next, _ = h.reserveWebsocketLease(1, "busy", func() {}, now.Add(time.Minute))
	victim := next.replacing
	if !victim.begin(now) {
		t.Fatal("old connection could not accept work during handshake")
	}
	if next.activate(now) {
		t.Fatal("busy connection was evicted")
	}
	next.release()
	if victim.retired || victim.claimedBy != nil {
		t.Fatal("failed admission damaged old connection")
	}
	victim.finish(now)
	for _, old := range original {
		old.release()
	}
	if _, total := h.websocketCounts(1); total != 0 {
		t.Fatal(total)
	}
}

func TestWebsocketReplacementAtGlobalCapacity(t *testing.T) {
	h := &Handler{}
	now := time.Now()
	var all []*websocketLease
	for user := int64(1); user <= 32; user++ {
		for i := 0; i < 4; i++ {
			all = append(all, h.acquireTestWebsocketLease(user, fmt.Sprint(i), func() {}, now))
		}
	}
	if lease, status := h.reserveWebsocketLease(99, "overflow", func() {}, now); lease != nil || status != 503 {
		t.Fatal("global cap not enforced", status)
	}
	replacement, status := h.reserveWebsocketLease(1, "replacement", func() {}, now.Add(time.Minute))
	if replacement == nil || status != 0 {
		t.Fatal("replacement needs an extra global slot", status)
	}
	victim := replacement.replacing
	replacement.release()
	if victim.retired {
		t.Fatal("failed handshake evicted victim at global capacity")
	}
	if _, total := h.websocketCounts(1); total != 128 {
		t.Fatal(total)
	}
	replacement, status = h.reserveWebsocketLease(1, "accepted", func() {}, now.Add(time.Minute))
	if status != 0 || replacement == nil {
		t.Fatal(status)
	}
	victim = replacement.replacing
	if !replacement.activate(now.Add(time.Minute)) {
		t.Fatal("replacement failed")
	}
	if !victim.retired {
		t.Fatal("victim not retired")
	}
	victim.release()
	if users, total := h.websocketCounts(1); users != 4 || total != 128 {
		t.Fatal(users, total)
	}
	replacement.release()
	for _, lease := range all {
		lease.release()
	}
	if _, total := h.websocketCounts(1); total != 0 {
		t.Fatal(total)
	}
}

func TestWebsocketVictimClosesDuringHandshake(t *testing.T) {
	h := &Handler{}
	now := time.Now()
	var leases []*websocketLease
	for i := 0; i < 4; i++ {
		leases = append(leases, h.acquireTestWebsocketLease(1, fmt.Sprint(i), func() {}, now))
	}
	next, _ := h.reserveWebsocketLease(1, "new", func() {}, now.Add(time.Minute))
	next.replacing.release()
	if next.replacing != nil || !next.counted {
		t.Fatal("reservation not transferred")
	}
	next.release()
	for _, lease := range leases {
		lease.release()
	}
	if _, total := h.websocketCounts(1); total != 0 {
		t.Fatal(total)
	}
}

func TestInvalidWebsocketHandshakePreservesIdleConnections(t *testing.T) {
	g, server := websocketGateway(t, func(http.ResponseWriter, *http.Request) { t.Error("unexpected inference") })
	var conns []*ws.Conn
	for i := 0; i < 4; i++ {
		conns = append(conns, dialGatewayWS(t, g, server))
	}
	g.h.wsSlotsMu.Lock()
	for _, lease := range g.h.wsSlots[g.user] {
		lease.last = time.Now().Add(-time.Minute)
	}
	g.h.wsSlotsMu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "GET", server.URL, nil)
	req.Header.Set("Authorization", "Bearer "+g.token)
	response, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode == 101 {
		t.Fatal("malformed handshake accepted")
	}
	headers := http.Header{"Authorization": []string{"Bearer " + g.token}, "Origin": []string{"https://foreign.example"}}
	conn, response, err := ws.Dial(ctx, "ws"+strings.TrimPrefix(server.URL, "http"), &ws.DialOptions{HTTPHeader: headers})
	if err == nil {
		conn.CloseNow()
		t.Fatal("foreign origin accepted")
	}
	if response == nil || response.StatusCode != 403 {
		t.Fatal("wrong origin rejection")
	}
	if users, total := g.h.websocketCounts(g.user); users != 4 || total != 4 {
		t.Fatal("failed handshake changed capacity", users, total)
	}
	for _, conn := range conns {
		// A continued protocol error exchange demonstrates the old sockets stayed open.
		sendWS(t, conn, `{"type":"response.create","input":"missing model"}`)
		if event := readWSTerminal(t, conn); string(event["type"]) != `"error"` {
			t.Fatal(event)
		}
	}
}

func TestClientHeartbeatTimeoutReleasesAdmission(t *testing.T) {
	h := &Handler{}
	done := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer close(done)
		ctx, cancel := context.WithCancel(r.Context())
		defer cancel()
		lease, _ := h.reserveWebsocketLease(1, "heartbeat", cancel, time.Now())
		defer lease.release()
		conn, err := ws.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer conn.CloseNow()
		if !lease.activate(time.Now()) {
			return
		}
		go maintainClientWebsocket(ctx, conn, "heartbeat", cancel, lease, 5*time.Millisecond, 30*time.Millisecond)
		_, _, _ = conn.Read(ctx)
	}))
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	conn, _, err := ws.Dial(ctx, "ws"+strings.TrimPrefix(server.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.CloseNow()
	// Deliberately do not read: no client reader is available to answer Ping.
	select {
	case <-done:
	case <-ctx.Done():
		t.Fatal("heartbeat did not release handler")
	}
	if users, total := h.websocketCounts(1); users != 0 || total != 0 {
		t.Fatal("heartbeat leaked admission", users, total)
	}
}

func TestWebsocketConcurrentAdmissionBounded(t *testing.T) {
	h := &Handler{}
	var wg sync.WaitGroup
	for i := 0; i < 256; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			lease, _ := h.reserveWebsocketLease(int64(i%40), fmt.Sprint(i), func() {}, time.Now())
			if lease == nil {
				return
			}
			defer lease.release()
			lease.activate(time.Now())
			h.wsSlotsMu.Lock()
			if h.wsSlotCount > 128 {
				t.Error("global cap exceeded")
			}
			for _, slots := range h.wsSlots {
				count := 0
				for _, slot := range slots {
					if slot.counted {
						count++
					}
				}
				if count > 4 {
					t.Error("user cap exceeded")
				}
			}
			h.wsSlotsMu.Unlock()
		}(i)
	}
	wg.Wait()
	if _, total := h.websocketCounts(1); total != 0 {
		t.Fatal(total)
	}
}
