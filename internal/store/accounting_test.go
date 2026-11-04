package store

import (
	"context"
	"strings"
	"testing"
	"time"
)

func accountingLog(id string, user, key int64, input, output int64) RequestLog {
	zero := int64(0)
	return RequestLog{ID: id, UserID: user, KeyID: key, Model: "model", Provider: "antigravity", Status: 502, Input: &input, Output: &output, Cached: &zero, CacheWrite: &zero, Error: "上游流未完整结束", CreatedAt: time.Now().Unix()}
}

func TestAutomaticSettlementUsesSnapshotAndReleasesUnknown(t *testing.T) {
	s, user, key := quotaStore(t)
	ctx := context.Background()
	now := time.Now()
	price := Price{Input: 1000000, Output: 2000000, Cached: 500000, CacheWrite: 3000000}
	if err := s.Reserve(ctx, "known", user, key, "model", price, 50, now); err != nil {
		t.Fatal(err)
	}
	l := accountingLog("known", user, key, 10, 3)
	cached, write := int64(2), int64(1)
	l.Cached = &cached
	l.CacheWrite = &write
	// Current model pricing is unrelated to the immutable request snapshot.
	if _, err := s.DB.Exec("INSERT INTO models(id,provider,upstream_id,synced_at) VALUES('model','antigravity','model',1)"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetModel(ctx, user, "model", true, &Price{Input: 99000000}); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if err := s.FinalizeRequest(ctx, l); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Reserve(ctx, "unknown", user, key, "model", price, 30, now); err != nil {
		t.Fatal(err)
	}
	u := RequestLog{ID: "unknown", UserID: user, KeyID: key, Model: "model", Provider: "antigravity", Status: 502, Error: "上游没有返回 usage", CreatedAt: now.Unix()}
	if err := s.FinalizeRequest(ctx, u); err != nil {
		t.Fatal(err)
	}
	windows, err := s.Quota(ctx, user)
	if err != nil {
		t.Fatal(err)
	}
	for _, w := range windows {
		if w.Used != 20 || w.Reserved != 0 {
			t.Fatal("incorrect settlement or duplicate charge", w)
		}
	}
	logs, err := s.Logs(ctx, user, false, 0, "", 0)
	if err != nil || len(logs) != 2 {
		t.Fatal("missing logs", err)
	}
	for _, l := range logs {
		if l.Amount == nil {
			t.Fatal("settled log left pending")
		}
		if l.ID == "unknown" && (*l.Amount != 0 || l.Input != nil || !strings.Contains(l.Error, unknownUsageNote)) {
			t.Fatal("unknown usage not explicitly zero-settled")
		}
	}
}

func TestSettlementFailurePreservesEvidenceAndRetriesAtomically(t *testing.T) {
	s, user, key := quotaStore(t)
	ctx := context.Background()
	if err := s.Reserve(ctx, "retry", user, key, "model", Price{Input: 1000000, Output: 1000000}, 50, time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.Exec(`CREATE TRIGGER fail_settlement BEFORE UPDATE ON quota_windows WHEN NEW.reserved<OLD.reserved BEGIN SELECT RAISE(ABORT,'forced settlement failure'); END`); err != nil {
		t.Fatal(err)
	}
	l := accountingLog("retry", user, key, 10, 5)
	l.CreatedAt = time.Now().Add(-40 * 24 * time.Hour).Unix()
	if err := s.FinalizeRequest(ctx, l); err == nil {
		t.Fatal("forced settlement failure ignored")
	}
	windows, _ := s.Quota(ctx, user)
	for _, w := range windows {
		if w.Reserved != 50 || w.Used != 0 {
			t.Fatal("partial ledger commit", w)
		}
	}
	pending, err := s.PendingCharges(ctx)
	if err != nil || len(pending) != 1 || !strings.Contains(pending[0].Error, settlementFailureNote) {
		t.Fatal("failed write not diagnosable", err, pending)
	}
	if err = s.CleanupLogs(ctx); err != nil {
		t.Fatal(err)
	}
	var count int
	if err = s.DB.QueryRow("SELECT count(*) FROM request_logs WHERE id='retry'").Scan(&count); err != nil || count != 1 {
		t.Fatal("pending financial evidence was pruned", err)
	}
	if _, err = s.DB.Exec("DROP TRIGGER fail_settlement"); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if err = s.RecoverInFlight(ctx); err != nil {
			t.Fatal(err)
		}
	}
	windows, _ = s.Quota(ctx, user)
	for _, w := range windows {
		if w.Used != 15 || w.Reserved != 0 {
			t.Fatal("retry failed or charged twice", w)
		}
	}
	logs, _ := s.Logs(ctx, user, false, 0, "", 0)
	if len(logs) != 1 || logs[0].Amount == nil || *logs[0].Amount != 15 || strings.Contains(logs[0].Error, "需要管理员核算") {
		t.Fatal("resolved settlement still shown as pending")
	}
}

func TestRecoverySettlesLegacyPendingAndIncompleteEvidence(t *testing.T) {
	s, user, key := quotaStore(t)
	ctx := context.Background()
	for _, id := range []string{"legacy-known", "legacy-unknown", "crash"} {
		if err := s.Reserve(ctx, id, user, key, "model", Price{Input: 1000000, Output: 1000000}, 20, time.Now()); err != nil {
			t.Fatal(err)
		}
		if id != "crash" {
			if err := s.Pending(ctx, id); err != nil {
				t.Fatal(err)
			}
		}
	}
	l := accountingLog("legacy-known", user, key, 7, 3)
	l.Error = "上游流被中断，用量待核算"
	if err := s.LogRequest(ctx, l); err != nil {
		t.Fatal(err)
	}
	partial := accountingLog("legacy-unknown", user, key, 99, 0)
	partial.Output = nil
	if err := s.LogRequest(ctx, partial); err != nil {
		t.Fatal(err)
	}
	if err := s.RecoverInFlight(ctx); err != nil {
		t.Fatal(err)
	}
	pending, _ := s.PendingCharges(ctx)
	if len(pending) != 0 {
		t.Fatal("legacy pending records were not processed")
	}
	windows, _ := s.Quota(ctx, user)
	for _, w := range windows {
		if w.Used != 10 || w.Reserved != 0 {
			t.Fatal("recovery charged partial evidence or lost snapshot usage", w)
		}
	}
	logs, _ := s.Logs(ctx, user, false, 0, "", 0)
	if len(logs) != 3 {
		t.Fatal("crash request lost")
	}
	for _, l := range logs {
		if l.Amount == nil {
			t.Fatal("recovery left null amount")
		}
		if l.ID != "legacy-known" && *l.Amount != 0 {
			t.Fatal("unknown usage charged")
		}
	}
}

func TestInvalidSnapshotRemainsManualAndNoPartialChargeOnLogFailure(t *testing.T) {
	s, user, key := quotaStore(t)
	ctx := context.Background()
	if err := s.Reserve(ctx, "bad-price", user, key, "model", Price{}, 20, time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.Exec("UPDATE request_charges SET price_snapshot='null' WHERE id='bad-price'"); err != nil {
		t.Fatal(err)
	}
	if err := s.FinalizeRequest(ctx, accountingLog("bad-price", user, key, 10, 2)); err == nil {
		t.Fatal("missing price snapshot treated as free")
	}
	if err := s.RecoverInFlight(ctx); err != nil {
		t.Fatal(err)
	}
	pending, _ := s.PendingCharges(ctx)
	if len(pending) != 1 {
		t.Fatal("corrupt financial snapshot silently discarded")
	}
	if err := s.Reserve(ctx, "bad-log", user, key, "model", Price{}, 20, time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.Exec(`CREATE TRIGGER fail_log BEFORE INSERT ON request_logs WHEN NEW.id='bad-log' BEGIN SELECT RAISE(ABORT,'forced log failure'); END`); err != nil {
		t.Fatal(err)
	}
	if err := s.FinalizeRequest(ctx, accountingLog("bad-log", user, key, 1, 2)); err == nil {
		t.Fatal("log failure ignored")
	}
	windows, _ := s.Quota(ctx, user)
	for _, w := range windows {
		if w.Used != 0 || w.Reserved != 40 {
			t.Fatal("charge committed without evidence", w)
		}
	}
}
