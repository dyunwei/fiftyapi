package gateway

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fiftyapi/internal/accounts"
	"fiftyapi/internal/store"
	"fmt"
	"math"
	"sort"
	"sync"
	"time"
)

type accountSlot struct {
	mu              sync.Mutex
	active, waiting int
	changed         chan struct{}
	queue           []*struct{ marker byte }
}

func (s *accountSlot) acquire(limit int) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.active >= limit || s.waiting > 0 {
		return false
	}
	s.active++
	return true
}
func (s *accountSlot) release() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.active > 0 {
		s.active--
	}
	if s.changed != nil {
		close(s.changed)
	}
	s.changed = make(chan struct{})
}
func (s *accountSlot) load() (int, int) { s.mu.Lock(); defer s.mu.Unlock(); return s.active, s.waiting }
func (s *accountSlot) notify() {
	if s.changed != nil {
		close(s.changed)
	}
	s.changed = make(chan struct{})
}
func (s *accountSlot) wait(ctx context.Context, p store.AccountPolicy) bool {
	if p.WaitMS == 0 || p.MaxQueue == 0 {
		return false
	}
	ticket := new(struct{ marker byte })
	s.mu.Lock()
	if len(s.queue) >= p.MaxQueue {
		s.mu.Unlock()
		return false
	}
	s.queue = append(s.queue, ticket)
	s.waiting = len(s.queue)
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		for i, v := range s.queue {
			if v == ticket {
				s.queue = append(s.queue[:i], s.queue[i+1:]...)
				s.waiting = len(s.queue)
				s.notify()
				break
			}
		}
	}()
	timer := time.NewTimer(time.Duration(p.WaitMS) * time.Millisecond)
	defer timer.Stop()
	for {
		s.mu.Lock()
		if ctx.Err() != nil {
			s.mu.Unlock()
			return false
		}
		if len(s.queue) > 0 && s.queue[0] == ticket && s.active < p.Concurrency {
			s.queue = s.queue[1:]
			s.waiting = len(s.queue)
			s.active++
			s.notify()
			s.mu.Unlock()
			return true
		}
		if s.changed == nil {
			s.changed = make(chan struct{})
		}
		changed := s.changed
		s.mu.Unlock()
		select {
		case <-ctx.Done():
			return false
		case <-timer.C:
			return false
		case <-changed:
		}
	}
}

func (h *Handler) slot(id int64) *accountSlot {
	v, _ := h.slots.LoadOrStore(id, &accountSlot{})
	return v.(*accountSlot)
}

type selection struct {
	ID         int64
	Route      store.RouteCandidate
	Credential accounts.Credential
	Release    func()
	Previous   int64
	Layer      string
}

func historyPortable(request map[string]json.RawMessage) bool {
	// Opaque thought/compaction state is account-associated. Plain text and
	// self-contained function calls/results may migrate; signatures never stripped.
	var inspect func(any) bool
	inspect = func(v any) bool {
		switch x := v.(type) {
		case map[string]any:
			if kind, ok := x["type"].(string); ok {
				switch kind {
				case "redacted_thinking", "compaction", "server_tool_use", "web_search_tool_result", "web_fetch_tool_result", "mcp_call":
					return false
				}
			}
			for k, value := range x {
				if k == "encrypted_content" || k == "signature" || k == "thoughtSignature" || k == "thought_signature" || k == "previous_response_id" {
					if value != nil && value != "" {
						return false
					}
				}
				if !inspect(value) {
					return false
				}
			}
		case []any:
			for _, value := range x {
				if !inspect(value) {
					return false
				}
			}
		}
		return true
	}
	for _, key := range []string{"input", "messages", "previous_response_id"} {
		if raw := request[key]; len(raw) > 0 {
			var v any
			if json.Unmarshal(raw, &v) != nil {
				return false
			}
			if key == "previous_response_id" && v != nil && v != "" {
				return false
			}
			if !inspect(v) {
				return false
			}
		}
	}
	return true
}
func (h *Handler) selectAccount(ctx context.Context, user int64, model, scope string, fingerprint []byte, portable bool, excluded map[int64]bool, seed string) (selection, error) {
	parentContext := ctx
	queueContext, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	ctx = queueContext
	rows, err := h.store.RouteCandidates(ctx, model, scope, requestProtocol(ctx))
	if err != nil {
		return selection{}, err
	}
	previous := int64(0)
	if len(fingerprint) > 0 {
		var hold int64
		previous, hold, err = h.store.GatewaySessionBinding(ctx, fingerprint)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return selection{}, err
		}
		if previous != 0 && hold > time.Now().Unix() {
			return selection{}, &scheduleUnavailable{status: 429, until: hold, message: "原会话账号暂时限流，请稍后重试或新建会话"}
		}
	}
	candidates := []store.RouteCandidate{}
	earliest := int64(0)
	now := time.Now()
	for _, c := range rows {
		if turn, ok := ctx.Value(websocketStateKey{}).(*websocketTurn); ok {
			if owner := turn.state.Owner(); owner != 0 && owner != c.ID {
				continue
			}
		}
		if excluded[c.ID] {
			continue
		}
		if c.CooldownUntil > now.Unix() {
			if earliest == 0 || c.CooldownUntil < earliest {
				earliest = c.CooldownUntil
			}
			continue
		}
		if until, ok := h.cooldowns.Load(c.ID); ok && now.Before(until.(time.Time)) {
			continue
		}
		candidates = append(candidates, c)
	}
	if !portable && previous == 0 && len(candidates) > 1 {
		return selection{}, errors.New("上下文包含账号关联的签名或加密状态，缺少原会话绑定；请新建会话")
	}
	if !portable && previous != 0 {
		filtered := candidates[:0]
		for _, c := range candidates {
			if c.ID == previous {
				filtered = append(filtered, c)
			}
		}
		candidates = filtered
	}
	if len(candidates) == 0 {
		return selection{}, &scheduleUnavailable{until: earliest, message: "没有可用账号：账号可能已限流、冷却、停用或需重新授权"}
	}
	score := func(c store.RouteCandidate) float64 {
		hash := sha256.Sum256([]byte(fmt.Sprintf("%s:%d", seed, c.ID)))
		u := (float64(binary.BigEndian.Uint64(hash[:8])>>11) + 1) / (1<<53 + 1)
		// Priority forms a strict layer. Within one layer, weighted exponential
		// racing gives every new session exactly the configured share over time.
		// Capacity and cooldown are gates, not hidden weight multipliers.
		return -math.Log(u) / float64(c.Policy.Weight)
	}
	scores := map[int64]float64{}
	for _, c := range candidates {
		scores[c.ID] = score(c)
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		if candidates[i].Policy.Priority != candidates[j].Policy.Priority {
			return candidates[i].Policy.Priority < candidates[j].Policy.Priority
		}
		return scores[candidates[i].ID] < scores[candidates[j].ID]
	})
	try := func(c store.RouteCandidate, wait bool, layer string) (selection, bool) {
		if ctx.Err() != nil {
			return selection{}, false
		}
		accessCtx, stopAccess := context.WithTimeout(ctx, 3*time.Second)
		credential, e := h.accounts.Access(accessCtx, c.ID)
		stopAccess()
		if e != nil {
			return selection{}, false
		}
		// Re-read after refresh/queue setup: disabling or changing a model must take
		// effect before dispatch, not after a stale account snapshot is selected.
		current, e := h.store.RouteCandidates(ctx, model, scope, requestProtocol(ctx))
		if e != nil {
			return selection{}, false
		}
		valid := false
		for _, fresh := range current {
			if fresh.ID == c.ID && fresh.CooldownUntil <= time.Now().Unix() {
				c = fresh
				valid = true
				break
			}
		}
		if !valid {
			return selection{}, false
		}
		slot := h.slot(c.ID)
		acquired := slot.acquire(c.Policy.Concurrency)
		if !acquired && wait {
			acquired = slot.wait(ctx, c.Policy)
		}
		if !acquired {
			return selection{}, false
		}
		var once sync.Once
		release := func() { once.Do(slot.release) }
		// Recheck authorization/routing after waiting. No upstream call is held in
		// a SQLite transaction and no canceled request starts a fresh inference.
		fresh, e := h.store.RouteCandidates(ctx, model, scope, requestProtocol(ctx))
		valid = false
		for _, row := range fresh {
			if row.ID == c.ID && row.CooldownUntil <= time.Now().Unix() {
				c = row
				active, _ := slot.load()
				valid = active <= row.Policy.Concurrency
				break
			}
		}
		if e != nil || !valid || ctx.Err() != nil {
			release()
			return selection{}, false
		}
		accessCtx, stopAccess = context.WithTimeout(ctx, time.Second)
		credential, e = h.accounts.Access(accessCtx, c.ID)
		stopAccess()
		if e != nil || ctx.Err() != nil {
			release()
			return selection{}, false
		}
		if len(fingerprint) > 0 {
			bound, e := h.store.ClaimGatewaySession(ctx, user, c.ID, fingerprint, previous, portable)
			if e != nil || bound != c.ID {
				release()
				return selection{}, false
			}
		}
		return selection{ID: c.ID, Route: c, Credential: credential, Release: release, Previous: previous, Layer: layer}, true
	}
	// Healthy affinity gets a bounded wait, then portable requests may escape.
	for _, c := range candidates {
		if c.ID == previous && (c.Failures < 2 || !portable) {
			if out, ok := try(c, true, "session"); ok {
				return out, nil
			}
			break
		}
	}
	for _, c := range candidates {
		if out, ok := try(c, false, "load_balance"); ok {
			return out, nil
		}
	}
	for _, c := range candidates {
		if out, ok := try(c, true, "queue"); ok {
			return out, nil
		}
		if ctx.Err() != nil {
			break
		}
	}
	if parentContext.Err() != nil {
		return selection{}, parentContext.Err()
	}
	return selection{}, &scheduleUnavailable{until: time.Now().Add(time.Second).Unix(), message: "账号并发或等待队列已满，请稍后重试"}
}

func (h *Handler) AccountLoad(id int64) (int, int) { return h.slot(id).load() }
func (h *Handler) ResetAccountCooldown(id int64)   { h.cooldowns.Delete(id) }

type scheduleUnavailable struct {
	status  int
	until   int64
	message string
}

func (e *scheduleUnavailable) Error() string { return e.message }

type requestProtocolKey struct{}

func requestProtocol(ctx context.Context) string {
	p, _ := ctx.Value(requestProtocolKey{}).(string)
	return p
}
