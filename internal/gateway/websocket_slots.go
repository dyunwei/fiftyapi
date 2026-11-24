package gateway

import (
	"context"
	"log"
	"time"
)

const websocketReplaceIdle = 30 * time.Second
const websocketExpireIdle = 5 * time.Minute

// pending covers both queued and executing turns. All lease state is protected
// by wsSlotsMu so admission cannot evict a socket while a new turn enters it.
type websocketLease struct {
	handler   *Handler
	user      int64
	id        string
	cancel    context.CancelFunc
	last      time.Time
	pending   int
	retired   bool
	ready     bool
	counted   bool
	replacing *websocketLease
	claimedBy *websocketLease
}

// Reserve capacity before the library handshake, but do not cancel an existing
// socket until the handshake succeeds. A replacement shares its victim's slot.
func (h *Handler) reserveWebsocketLease(user int64, id string, cancel context.CancelFunc, now time.Time) (*websocketLease, int) {
	h.wsSlotsMu.Lock()
	defer h.wsSlotsMu.Unlock()
	if h.wsSlots == nil {
		h.wsSlots = make(map[int64]map[string]*websocketLease)
	}
	slots := h.wsSlots[user]
	used := 0
	for _, slot := range slots {
		if slot.counted {
			used++
		}
	}
	var victim *websocketLease
	if used >= h.websocketConnectionLimit() {
		for _, slot := range slots {
			if slot.ready && slot.claimedBy == nil && slot.pending == 0 && now.Sub(slot.last) >= websocketReplaceIdle && (victim == nil || slot.last.Before(victim.last)) {
				victim = slot
			}
		}
		if victim == nil {
			return nil, 429
		}
	}
	if victim == nil && h.wsSlotCount >= 128 {
		return nil, 503
	}
	if slots == nil {
		slots = make(map[string]*websocketLease)
		h.wsSlots[user] = slots
	}
	lease := &websocketLease{handler: h, user: user, id: id, cancel: cancel, last: now, replacing: victim, counted: victim == nil}
	if victim != nil {
		victim.claimedBy = lease
	} else {
		h.wsSlotCount++
	}
	slots[id] = lease
	return lease, 0
}
func (l *websocketLease) activate(now time.Time) bool {
	l.handler.wsSlotsMu.Lock()
	defer l.handler.wsSlotsMu.Unlock()
	if l.retired {
		return false
	}
	if l.replacing != nil {
		victim := l.replacing
		// Activity may resume while the handshake is in progress. Protect old work.
		if victim.pending != 0 || now.Sub(victim.last) < websocketReplaceIdle {
			return false
		}
		victim.retireLocked("replaced_idle", now)
	}
	l.ready = true
	l.last = now
	return true
}
func (h *Handler) websocketCounts(user int64) (int, int) {
	h.wsSlotsMu.Lock()
	defer h.wsSlotsMu.Unlock()
	used := 0
	for _, slot := range h.wsSlots[user] {
		if slot.counted {
			used++
		}
	}
	return used, h.wsSlotCount
}
func (l *websocketLease) retireLocked(reason string, now time.Time) {
	if l.retired {
		return
	}
	l.retired = true
	if l.replacing != nil {
		l.replacing.claimedBy = nil
		l.replacing = nil
	}
	if l.counted {
		l.handler.wsSlotCount--
		l.counted = false
	}
	// If a victim closes naturally during a handshake, transfer its reservation.
	if l.claimedBy != nil {
		next := l.claimedBy
		next.replacing = nil
		next.counted = true
		l.handler.wsSlotCount++
		l.claimedBy = nil
	}
	slots := l.handler.wsSlots[l.user]
	delete(slots, l.id)
	if len(slots) == 0 {
		delete(l.handler.wsSlots, l.user)
	}
	log.Printf("WebSocket slot released connection_id=%s user_id=%d reason=%s idle_ms=%d pending=%d", l.id, l.user, reason, now.Sub(l.last).Milliseconds(), l.pending)
	l.cancel()
}
func (l *websocketLease) release() {
	l.handler.wsSlotsMu.Lock()
	defer l.handler.wsSlotsMu.Unlock()
	l.retireLocked("closed", time.Now())
}
func (l *websocketLease) begin(now time.Time) bool {
	l.handler.wsSlotsMu.Lock()
	defer l.handler.wsSlotsMu.Unlock()
	if l.retired || !l.ready {
		return false
	}
	l.pending++
	l.last = now
	return true
}
func (l *websocketLease) finish(now time.Time) {
	l.handler.wsSlotsMu.Lock()
	defer l.handler.wsSlotsMu.Unlock()
	l.pending--
	l.last = now
}
func (l *websocketLease) expire(now time.Time) bool {
	l.handler.wsSlotsMu.Lock()
	defer l.handler.wsSlotsMu.Unlock()
	if l.retired {
		return true
	}
	if l.ready && l.claimedBy == nil && l.pending == 0 && now.Sub(l.last) >= websocketExpireIdle {
		l.retireLocked("idle_timeout", now)
		return true
	}
	return false
}

// Preserve the default for handlers constructed directly by tests.
func (h *Handler) websocketConnectionLimit() int {
	if h.websocketUserLimit > 0 {
		return h.websocketUserLimit
	}
	return 32
}
