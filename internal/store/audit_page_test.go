package store

import (
	"context"
	"testing"
)

func TestAuditPaginationAndFilters(t *testing.T) {
	s, user, _ := quotaStore(t)
	ctx := context.Background()
	for i := 0; i < 25; i++ {
		if _, err := s.DB.ExecContext(ctx, "INSERT INTO admin_audit_logs(actor_id,action,target_id,details,created_at) VALUES(?,?,?,?,?)", user, "test.update", 123, `{"note":"literal_%"}`, 1000+i); err != nil {
			t.Fatal(err)
		}
	}
	f := AuditFilter{ActorID: user, TargetID: 123, Action: "test.up", Details: "_%", Since: 1000, Until: 1024}
	first, err := s.AuditListPage(ctx, f, 1, 10)
	if err != nil || first.Total != 25 || len(first.Items) != 10 {
		t.Fatal(err, first)
	}
	second, err := s.AuditListPage(ctx, f, 2, 10)
	if err != nil || len(second.Items) != 10 || first.Items[0]["id"] == second.Items[0]["id"] {
		t.Fatal("overlap", err)
	}
	last, err := s.AuditListPage(ctx, f, 3, 10)
	if err != nil || len(last.Items) != 5 {
		t.Fatal("last page", err, last)
	}
	f.Since = 1020
	result, err := s.AuditListPage(ctx, f, 1, 10)
	if err != nil || result.Total != 5 {
		t.Fatal("time filter", err, result)
	}
	f.TargetID = 999
	result, err = s.AuditListPage(ctx, f, 1, 10)
	if err != nil || result.Total != 0 || result.Items == nil {
		t.Fatal("empty filter", err, result)
	}
	if _, err = s.AuditListPage(ctx, f, 0, 10); err == nil {
		t.Fatal("invalid page accepted")
	}
}
