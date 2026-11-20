package store

import (
	"context"
	"testing"
	"time"
)

func TestMaintenanceTodoAlertsDeduplicateAndRearm(t *testing.T) {
	s, user, key := quotaStore(t)
	ctx := context.Background()
	now := time.Now()
	account, err := s.CreateAccount(ctx, user, "fixture", "openai", func(int64) ([]byte, error) {
		return []byte("fixture"), nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.AccountState(ctx, account, "reauth_required", ""); err != nil {
		t.Fatal(err)
	}
	if err = s.Reserve(ctx, "pending", user, key, "model", Price{}, 1, now); err != nil {
		t.Fatal(err)
	}
	if err = s.Pending(ctx, "pending"); err != nil {
		t.Fatal(err)
	}
	if _, err = s.DB.Exec("INSERT INTO backup_records(id,status,filename,object_key,target,trigger_kind,started_at,expires_at) VALUES('fixture','failed','','',X'','manual',1,2)"); err != nil {
		t.Fatal(err)
	}
	counts, err := s.MaintenanceTodo(ctx)
	if err != nil || counts["reauth"] != 1 || counts["settlement"] != 1 || counts["backup"] != 1 {
		t.Fatalf("incorrect maintenance tasks: %v, %v", counts, err)
	}
	issues, err := s.UnsentMaintenanceIssues(ctx)
	if err != nil || len(issues) != 3 {
		t.Fatalf("missing alerts: %v, %v", issues, err)
	}
	if err = s.MarkMaintenanceIssuesSent(ctx, issues); err != nil {
		t.Fatal(err)
	}
	if issues, err = s.UnsentMaintenanceIssues(ctx); err != nil || len(issues) != 0 {
		t.Fatalf("alerts not deduplicated: %v, %v", issues, err)
	}
	if err := s.AccountState(ctx, account, "ready", ""); err != nil {
		t.Fatal(err)
	}
	if _, err = s.UnsentMaintenanceIssues(ctx); err != nil {
		t.Fatal(err)
	}
	if err := s.AccountState(ctx, account, "reauth_required", ""); err != nil {
		t.Fatal(err)
	}
	if issues, err = s.UnsentMaintenanceIssues(ctx); err != nil || len(issues) != 1 || issues[0].Kind != "reauth" {
		t.Fatalf("reopened alert missing: %v, %v", issues, err)
	}
}
