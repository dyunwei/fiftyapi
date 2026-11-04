package store

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRoutingHealthPersistenceAndDeletedAccountCannotRevive(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "db")
	s, e := Open(ctx, path)
	if e != nil {
		t.Fatal(e)
	}
	defer func() { s.Close() }()
	actor, _ := s.CreateAdmin(ctx, "admin", "hash")
	other, _ := s.CreateUser(ctx, actor, "other", "hash", 100, 100)
	_ = other
	account, e := s.CreateAccount(ctx, actor, "account", "openai", func(int64) ([]byte, error) { return []byte("encrypted-original"), nil })
	if e != nil {
		t.Fatal(e)
	}
	if e = s.SyncModels(ctx, account, "openai", []string{"model"}); e != nil {
		t.Fatal(e)
	}
	if e = s.AccountState(ctx, account, "catalog_ready", ""); e != nil {
		t.Fatal(e)
	}
	if e = s.SetLimits(ctx, actor, actor, 10000, 10000); e != nil {
		t.Fatal(e)
	}
	key, _, e := s.CreateAPIKey(ctx, actor, "test")
	if e != nil {
		t.Fatal(e)
	}
	price := Price{Input: 1000000, Output: 1000000}
	if e = s.SetModel(ctx, actor, "model", true, &price); e != nil {
		t.Fatal(e)
	}
	if e = s.Reserve(ctx, "request", actor, key.ID, "model", price, 100, time.Now()); e != nil {
		t.Fatal(e)
	}
	percent := 100.0
	reset := time.Now().Add(time.Hour).Unix()
	observation := AccountObservation{RequestAttempt: RequestAttempt{RequestID: "request", Attempt: 1, AccountID: account, Model: "model", Status: 429, Retryable: true, Reason: "上游限流，等待恢复", CreatedAt: time.Now().Unix()}, CooldownUntil: reset, PrimaryUsed: &percent, PrimaryReset: &reset}
	if e = s.RecordAttempt(ctx, observation); e != nil {
		t.Fatal(e)
	}
	if e = s.RecordAttempt(ctx, observation); e == nil {
		t.Fatal("duplicate attempt accepted")
	}
	observation.Attempt = 2
	observation.Status = 200
	observation.Success = true
	observation.CooldownUntil = 0
	observation.PrimaryUsed = nil
	observation.PrimaryReset = nil
	if e = s.RecordAttempt(ctx, observation); e != nil {
		t.Fatal(e)
	}
	health, e := s.AccountHealth(ctx, account)
	if e != nil || len(health.Cooldowns) != 1 || health.Cooldowns[0].Until != reset || health.PrimaryUsed == nil || *health.PrimaryUsed != 100 {
		t.Fatal("success shortened cooldown or erased quota")
	}
	s.Close()
	s, e = Open(ctx, path)
	if e != nil {
		t.Fatal(e)
	}
	candidates, e := s.RouteCandidates(ctx, "model", "generate:model")
	if e != nil || len(candidates) != 1 || candidates[0].CooldownUntil != reset {
		t.Fatal("restart lost routing health")
	}
	if _, e = s.RequestAttempts(ctx, other, false, "request"); e != ErrNotFound {
		t.Fatal("other user could inspect routing attempts")
	}
	if e = s.DeleteAccount(ctx, actor, account); e != nil {
		t.Fatal(e)
	}
	if e = s.UpdateAccountCredentials(ctx, account, []byte("new-secret")); e == nil {
		t.Fatal("late refresh revived deleted credentials")
	}
	if e = s.AccountState(ctx, account, "catalog_ready", ""); e == nil {
		t.Fatal("late verification revived deleted account")
	}
	if e = s.SyncModels(ctx, account, "openai", []string{"model"}); e == nil {
		t.Fatal("deleted membership recreated")
	}
	if e = s.EnableAccount(ctx, actor, account, true); e == nil {
		t.Fatal("deleted account re-enabled")
	}
	var raw []byte
	if e = s.DB.QueryRow("SELECT credentials FROM upstream_accounts WHERE id=?", account).Scan(&raw); e != nil || strings.Contains(string(raw), "original") || strings.Contains(string(raw), "new-secret") {
		t.Fatal("deleted credential wipe failed")
	}
	zero := int64(0)
	if e = s.FinalizeRequest(ctx, RequestLog{ID: "request", UserID: actor, KeyID: key.ID, AccountID: &account, Model: "model", Provider: "openai", Status: 200, Input: &zero, Output: &zero, CreatedAt: time.Now().Unix()}); e != nil {
		t.Fatal("deletion broke in-flight financial linkage")
	}
	attempts, e := s.RequestAttempts(ctx, actor, true, "request")
	if e != nil || len(attempts) != 2 {
		t.Fatal("deletion lost historical attempts")
	}
	rows, e := s.Accounts(ctx)
	if e != nil || len(rows) != 0 {
		t.Fatal("deleted account remains in account management")
	}
}
func TestRoutingPolicyAndResetAuditRollback(t *testing.T) {
	ctx := context.Background()
	s, e := Open(ctx, filepath.Join(t.TempDir(), "db"))
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	actor, _ := s.CreateAdmin(ctx, "admin", "hash")
	id, e := s.CreateAccount(ctx, actor, "account", "openai", func(int64) ([]byte, error) { return []byte("ciphertext"), nil })
	if e != nil {
		t.Fatal(e)
	}
	p := DefaultAccountPolicy()
	p.Weight = 101
	if e = s.SetAccountPolicy(ctx, actor, id, p); e == nil {
		t.Fatal("invalid policy accepted")
	}
	p.Weight = 4
	p.Concurrency = 8
	if e = s.SetAccountPolicy(ctx, actor, id, p); e != nil {
		t.Fatal(e)
	}
	if e = s.InitializeAccountPolicy(ctx, id, 2); e != nil {
		t.Fatal(e)
	}
	got, _ := s.AccountPolicy(ctx, id)
	if got.Weight != 4 || got.Concurrency != 8 {
		t.Fatal("legacy initialization overwrote explicit policy")
	}
	percent := 100.0
	reset := time.Now().Add(time.Hour).Unix()
	if e = s.SaveQuotaSnapshot(ctx, id, &percent, &reset, nil, nil); e != nil {
		t.Fatal(e)
	}
	if _, e = s.DB.Exec(`CREATE TRIGGER reject_routing_audit BEFORE INSERT ON admin_audit_logs BEGIN SELECT RAISE(ABORT,'fixture'); END`); e != nil {
		t.Fatal(e)
	}
	if e = s.ResetAccountHealth(ctx, actor, id); e == nil {
		t.Fatal("unaudited reset committed")
	}
	health, _ := s.AccountHealth(ctx, id)
	if len(health.Cooldowns) != 1 {
		t.Fatal("reset rollback lost cooldown")
	}
	if _, e = s.DB.Exec("DROP TRIGGER reject_routing_audit"); e != nil {
		t.Fatal(e)
	}
	if e = s.ResetAccountHealth(ctx, actor, id); e != nil {
		t.Fatal(e)
	}
	health, _ = s.AccountHealth(ctx, id)
	if len(health.Cooldowns) != 0 || health.PrimaryUsed == nil || *health.PrimaryUsed != 100 {
		t.Fatal("manual reset changed quota evidence")
	}
}

func TestQuotaWithoutResetStillCoolsAccount(t *testing.T) {
	ctx := context.Background()
	s, e := Open(ctx, filepath.Join(t.TempDir(), "db"))
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	actor, _ := s.CreateAdmin(ctx, "admin", "hash")
	id, e := s.CreateAccount(ctx, actor, "quota", "openai", func(int64) ([]byte, error) { return []byte("encrypted"), nil })
	if e != nil {
		t.Fatal(e)
	}
	used := 100.0
	if e = s.SaveQuotaSnapshot(ctx, id, &used, nil, nil, nil); e != nil {
		t.Fatal(e)
	}
	health, e := s.AccountHealth(ctx, id)
	if e != nil || len(health.Cooldowns) != 1 || health.Cooldowns[0].Until < time.Now().Unix()+55 {
		t.Fatal("quota exhaustion without reset did not create bounded cooldown")
	}
}
