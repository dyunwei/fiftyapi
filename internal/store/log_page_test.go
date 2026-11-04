package store

import (
	"context"
	"fmt"
	"testing"
	"time"
)

func TestLogPaginationFiltersOwnershipAndMissingExceptionLog(t *testing.T) {
	s, user, key := quotaStore(t)
	ctx := context.Background()
	other, err := s.CreateUser(ctx, user, "other", "hash", 100, 1000)
	if err != nil {
		t.Fatal(err)
	}
	otherKey, _, err := s.CreateAPIKey(ctx, other, "other")
	if err != nil {
		t.Fatal(err)
	}
	zero := int64(0)
	for i := 0; i < 45; i++ {
		owner, k := user, key
		if i >= 25 {
			owner = other
			k = otherKey.ID
		}
		status := 200
		if i%2 != 0 {
			status = 502
		}
		if err = s.LogRequest(ctx, RequestLog{ID: fmt.Sprintf("log-%02d", i), UserID: owner, KeyID: k, Model: fmt.Sprintf("model-%d", i%2), Provider: "antigravity", Status: status, Amount: &zero, CreatedAt: 1700000000}); err != nil {
			t.Fatal(err)
		}
	}
	for _, tc := range []struct {
		since, until int64
		total        int
	}{{1700000000, 1700000000, 45}, {0, 1699999999, 0}, {1700000001, 1700000002, 0}} {
		result, err := s.LogListPage(ctx, LogFilter{Admin: true, Since: tc.since, Until: tc.until}, 1, 10)
		if err != nil || result.Total != tc.total {
			t.Fatal("time range boundary", tc, err, result.Total)
		}
	}
	f := LogFilter{Admin: true}
	p1, err := s.LogListPage(ctx, f, 1, 20)
	if err != nil || p1.Total != 45 || len(p1.Items) != 20 {
		t.Fatal("first page", err, p1.Total)
	}
	p2, err := s.LogListPage(ctx, f, 2, 20)
	if err != nil || len(p2.Items) != 20 {
		t.Fatal("second page", err)
	}
	seen := map[string]bool{}
	for _, l := range p1.Items {
		seen[l.ID] = true
	}
	for _, l := range p2.Items {
		if seen[l.ID] {
			t.Fatal("unstable ordering at equal timestamps")
		}
	}
	member, err := s.LogListPage(ctx, LogFilter{User: user}, 2, 20)
	if err != nil || member.Total != 25 || len(member.Items) != 5 {
		t.Fatal("ownership/page", err)
	}
	for _, l := range member.Items {
		if l.UserID != user {
			t.Fatal("cross-user log leak")
		}
	}
	filtered, err := s.LogListPage(ctx, LogFilter{Admin: true, Status: 502, Model: "model-1"}, 1, 100)
	if err != nil || filtered.Total != 22 {
		t.Fatal("filters lost", err, filtered.Total)
	}
	private, err := s.LogListPage(ctx, LogFilter{User: other, RequestID: "log-00"}, 1, 20)
	if err != nil || private.Total != 0 {
		t.Fatal("ID filter bypassed ownership", err)
	}
	if err = s.Reserve(ctx, "ledger-only", user, key, "model", Price{}, 10, time.Now()); err != nil {
		t.Fatal(err)
	}
	if err = s.Pending(ctx, "ledger-only"); err != nil {
		t.Fatal(err)
	}
	exceptions, err := s.LogListPage(ctx, LogFilter{Admin: true, Settlement: "pending"}, 1, 20)
	if err != nil || exceptions.Total != 1 || exceptions.Items[0].ID != "ledger-only" || exceptions.Items[0].Reservation == nil || exceptions.Items[0].Status != 0 {
		t.Fatal("exception with failed log write disappeared", err, exceptions)
	}
	excluded, err := s.LogListPage(ctx, LogFilter{Admin: true, Settlement: "pending", Until: 1700000000}, 1, 10)
	if err != nil || excluded.Total != 0 {
		t.Fatal("time range failed for ledger-only log", err, excluded)
	}
	if err = s.ManualSettle(ctx, user, "ledger-only", 0, "确认释放预占"); err != nil {
		t.Fatal(err)
	}
	after, err := s.LogListPage(ctx, f, 1, 100)
	if err != nil || after.Total != 46 {
		t.Fatal("ledger/log union duplicated a recovered row", err, after.Total)
	}
	if _, err = s.LogListPage(ctx, f, 0, 20); err == nil {
		t.Fatal("invalid page accepted")
	}
}

func TestRetrySettlementAuditsAndRejectsInflight(t *testing.T) {
	s, user, key := quotaStore(t)
	ctx := context.Background()
	for _, id := range []string{"retry-ui", "active"} {
		if err := s.Reserve(ctx, id, user, key, "model", Price{Input: 1000000}, 20, time.Now()); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.RetrySettlement(ctx, user, "active"); err == nil {
		t.Fatal("running request settled by retry")
	}
	if err := s.Pending(ctx, "retry-ui"); err != nil {
		t.Fatal(err)
	}
	if err := s.LogRequest(ctx, accountingLog("retry-ui", user, key, 10, 0)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.Exec(`CREATE TRIGGER fail_retry_audit BEFORE INSERT ON admin_audit_logs WHEN NEW.action='retry_settle' BEGIN SELECT RAISE(ABORT,'forced audit failure'); END`); err != nil {
		t.Fatal(err)
	}
	if err := s.RetrySettlement(ctx, user, "retry-ui"); err == nil {
		t.Fatal("retry committed without audit")
	}
	q, _ := s.Quota(ctx, user)
	if q[0].Used != 0 || q[0].Reserved != 40 {
		t.Fatal("audit failure partially committed ledger", q)
	}
	if _, err := s.DB.Exec("DROP TRIGGER fail_retry_audit"); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if err := s.RetrySettlement(ctx, user, "retry-ui"); err != nil {
			t.Fatal(err)
		}
	}
	q, _ = s.Quota(ctx, user)
	if q[0].Used != 10 || q[0].Reserved != 20 {
		t.Fatal("retry charged twice or affected active request", q)
	}
	var count int
	if err := s.DB.QueryRow("SELECT count(*) FROM admin_audit_logs WHERE action='retry_settle'").Scan(&count); err != nil || count != 1 {
		t.Fatal("missing or duplicated retry audit", err, count)
	}
}
