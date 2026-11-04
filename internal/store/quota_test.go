package store

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func quotaStore(t *testing.T) (*Store, int64, int64) {
	t.Helper()
	s, e := Open(context.Background(), filepath.Join(t.TempDir(), "db"))
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { s.Close() })
	user, e := s.CreateAdmin(context.Background(), "admin", "hash")
	if e != nil {
		t.Fatal(e)
	}
	if e = s.SetLimits(context.Background(), user, user, 100, 1000); e != nil {
		t.Fatal(e)
	}
	key, _, e := s.CreateAPIKey(context.Background(), user, "test")
	if e != nil {
		t.Fatal(e)
	}
	return s, user, key.ID
}
func TestConcurrentQuotaReservation(t *testing.T) {
	s, user, key := quotaStore(t)
	ctx := context.Background()
	var wg sync.WaitGroup
	results := make(chan error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			results <- s.Reserve(ctx, string(rune('a'+i)), user, key, "model", Price{}, 100, time.Now())
		}(i)
	}
	wg.Wait()
	close(results)
	success := 0
	for e := range results {
		if e == nil {
			success++
		} else if !errors.Is(e, ErrQuota) {
			t.Fatal(e)
		}
	}
	if success != 1 {
		t.Fatalf("reserved %d requests", success)
	}
}
func TestWindowGenerationAndSettlement(t *testing.T) {
	s, user, key := quotaStore(t)
	ctx := context.Background()
	now := time.Now()
	p := Price{Input: 1000000}
	if e := s.Reserve(ctx, "first", user, key, "model", p, 100, now); e != nil {
		t.Fatal(e)
	}
	q, _ := s.Quota(ctx, user)
	if q[0].StartedAt != nil {
		t.Fatal("reservation started billing window")
	}
	if e := s.Settle(ctx, "first", 100, now); e != nil {
		t.Fatal(e)
	}
	if e := s.Settle(ctx, "first", 100, now); e != nil {
		t.Fatal(e)
	}
	if e := s.Settle(ctx, "first", 101, now); e == nil {
		t.Fatal("conflicting settlement accepted")
	}
	if e := s.Reserve(ctx, "blocked", user, key, "model", p, 1, now); !errors.Is(e, ErrQuota) {
		t.Fatalf("exhausted window: %v", e)
	}
	if e := s.Reserve(ctx, "old-generation", user, key, "model", p, 20, now.Add(6*time.Hour)); e != nil {
		t.Fatal(e)
	}
	if e := s.ResetQuota(ctx, user, user, "both"); e != nil {
		t.Fatal(e)
	}
	if e := s.Settle(ctx, "old-generation", 20, now.Add(6*time.Hour)); e != nil {
		t.Fatal(e)
	}
	q, _ = s.Quota(ctx, user)
	for _, w := range q {
		if w.Used != 0 || w.Reserved != 0 || w.StartedAt != nil {
			t.Fatal("old request charged a reset window")
		}
	}
	if e := s.Reserve(ctx, "free", user, key, "model", p, 10, now); e != nil {
		t.Fatal(e)
	}
	s.Settle(ctx, "free", 0, now)
	q, _ = s.Quota(ctx, user)
	if q[0].StartedAt != nil {
		t.Fatal("zero-cost response started window")
	}
	if e := s.Reserve(ctx, "unknown", user, key, "model", p, 100, now); e != nil {
		t.Fatal(e)
	}
	if e := s.RecoverInFlight(ctx); e != nil {
		t.Fatal(e)
	}
	pending, _ := s.PendingCharges(ctx)
	if len(pending) != 0 {
		t.Fatal("crash recovery retained unknown usage")
	}
	q, _ = s.Quota(ctx, user)
	if q[0].Reserved != 0 {
		t.Fatal("crash recovery failed to release uncertain usage")
	}
}
func TestPriceRounding(t *testing.T) {
	p := Price{Input: 100000, Output: 400000, Cached: 10000, CacheWrite: 125000}
	cost, e := p.Cost(TokenUsage{Input: 1, Output: 1, Cached: 1, CacheWrite: 1})
	if e != nil || cost != 1 {
		t.Fatalf("rounding: %d %v", cost, e)
	}
	cost, e = p.Cost(TokenUsage{Input: 1000000, Output: 1000000})
	if e != nil || cost != 500000 {
		t.Fatal("incorrect million-token pricing")
	}
	if _, e = p.Cost(TokenUsage{Input: -1}); e == nil {
		t.Fatal("negative usage accepted")
	}
}

func TestManualSettlementAtomicAndConcurrent(t *testing.T) {
	s, user, key := quotaStore(t)
	ctx := context.Background()
	if err := s.Reserve(ctx, "manual", user, key, "model", Price{Input: 1000000}, 100, time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := s.Pending(ctx, "manual"); err != nil {
		t.Fatal(err)
	}
	if err := s.LogRequest(ctx, RequestLog{ID: "manual", UserID: user, KeyID: key, Model: "model", Provider: "antigravity", Status: 502, CreatedAt: time.Now().Unix(), Error: "stream interrupted"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.Exec(`CREATE TRIGGER fail_manual_audit BEFORE INSERT ON admin_audit_logs WHEN NEW.action='manual_settle' BEGIN SELECT RAISE(ABORT,'test audit write failure'); END`); err != nil {
		t.Fatal(err)
	}
	if err := s.ManualSettle(ctx, user, "manual", 30, "verified usage"); err == nil {
		t.Fatal("audit failure did not roll back settlement")
	}
	windows, err := s.Quota(ctx, user)
	if err != nil {
		t.Fatal(err)
	}
	if windows[0].Used != 0 || windows[0].Reserved != 100 {
		t.Fatal("partial manual settlement persisted")
	}
	if _, err = s.DB.Exec("DROP TRIGGER fail_manual_audit"); err != nil {
		t.Fatal(err)
	}
	results := make(chan error, 8)
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); results <- s.ManualSettle(ctx, user, "manual", 30, "verified usage") }()
	}
	wg.Wait()
	close(results)
	success := 0
	for err := range results {
		if err == nil {
			success++
		}
	}
	if success != 1 {
		t.Fatalf("manual settlements succeeded %d times", success)
	}
	var audits int
	if err = s.DB.QueryRow("SELECT count(*) FROM admin_audit_logs WHERE action='manual_settle'").Scan(&audits); err != nil || audits != 1 {
		t.Fatal("successful settlement audit count mismatch")
	}
	logs, err := s.LogsPage(ctx, user, false, 0, "", 0, "", 0, "manual")
	if err != nil || len(logs) != 1 || logs[0].Amount == nil || *logs[0].Amount != 30 {
		t.Fatal("log did not update atomically")
	}
	windows, err = s.Quota(ctx, user)
	if err != nil || windows[0].Used != 30 || windows[0].Reserved != 0 {
		t.Fatal("manual quota not reconciled")
	}
}
