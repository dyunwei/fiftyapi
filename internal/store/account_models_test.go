package store

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"
)

func TestAccountModelsIsolationPagingAndSyncHistory(t *testing.T) {
	ctx := context.Background()
	s, e := Open(ctx, filepath.Join(t.TempDir(), "db"))
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	actor, e := s.CreateAdmin(ctx, "admin", "hash")
	if e != nil {
		t.Fatal(e)
	}
	create := func(name string) int64 {
		t.Helper()
		id, e := s.CreateAccount(ctx, actor, name, "openai", func(int64) ([]byte, error) { return []byte("encrypted-placeholder"), nil })
		if e != nil {
			t.Fatal(e)
		}
		return id
	}
	a, b := create("first"), create("second")
	ids := []string{}
	for i := 0; i < 45; i++ {
		ids = append(ids, fmt.Sprintf("fixture-%02d", i))
	}
	if e = s.SyncModels(ctx, a, "openai", ids); e != nil {
		t.Fatal(e)
	}
	if e = s.AccountState(ctx, a, "catalog_ready", ""); e != nil {
		t.Fatal(e)
	}
	if e = s.SetModel(ctx, actor, ids[0], true, &Price{Input: 1, Output: 1}); e != nil {
		t.Fatal(e)
	}
	if e = s.SetModel(ctx, actor, ids[1], false, nil); e != nil {
		t.Fatal(e)
	}
	first, e := s.AccountModelsPage(ctx, a, 1, 20, "")
	if e != nil || first.Total != 45 || len(first.Items) != 20 || first.SyncedAt == nil {
		t.Fatalf("first page: %+v %v", first, e)
	}
	if !first.Items[0].Priced || !first.Items[0].Routable || first.Items[1].Enabled || first.Items[2].Priced {
		t.Fatal("configuration flags incorrect")
	}
	seen := map[string]bool{}
	for page := 1; page <= 3; page++ {
		p, e := s.AccountModelsPage(ctx, a, page, 20, "")
		if e != nil {
			t.Fatal(e)
		}
		for _, m := range p.Items {
			if seen[m.ID] {
				t.Fatal("duplicated model between pages")
			}
			seen[m.ID] = true
		}
	}
	if len(seen) != 45 {
		t.Fatal("paging lost models")
	}
	p, e := s.AccountModelsPage(ctx, a, 1, 20, "FIXTURE-04")
	if e != nil || p.Total != 1 || p.Items[0].UpstreamID != "fixture-04" {
		t.Fatal("search failed")
	}
	if _, e = s.DB.Exec("UPDATE upstream_accounts SET models_synced_at=123 WHERE id=?", a); e != nil {
		t.Fatal(e)
	}
	if e = s.SyncModels(ctx, b, "openai", []string{ids[0], "other-model"}); e != nil {
		t.Fatal(e)
	}
	p, e = s.AccountModelsPage(ctx, a, 1, 20, "")
	if e != nil || *p.SyncedAt != 123 || p.Total != 45 {
		t.Fatal("another account changed this account's catalog metadata")
	}
	p, e = s.AccountModelsPage(ctx, b, 1, 20, "")
	if e != nil || p.Total != 2 {
		t.Fatal("models leaked between accounts")
	}
	if _, e = s.DB.Exec(`CREATE TRIGGER reject_sync BEFORE UPDATE OF models_synced_at ON upstream_accounts BEGIN SELECT RAISE(ABORT,'test sync failure'); END`); e != nil {
		t.Fatal(e)
	}
	if e = s.SyncModels(ctx, a, "openai", []string{"replacement"}); e == nil {
		t.Fatal("failed synchronization committed")
	}
	p, e = s.AccountModelsPage(ctx, a, 1, 20, "")
	if e != nil || p.Total != 45 || *p.SyncedAt != 123 {
		t.Fatal("sync failure erased the last successful snapshot")
	}
	if _, e = s.DB.Exec("DROP TRIGGER reject_sync"); e != nil {
		t.Fatal(e)
	}
	if e = s.SyncModels(ctx, a, "openai", nil); e != nil {
		t.Fatal(e)
	}
	p, e = s.AccountModelsPage(ctx, a, 1, 20, "")
	if e != nil || p.Total != 0 || p.Items == nil || p.SyncedAt == nil {
		t.Fatal("empty upstream catalog not represented")
	}
	accounts, e := s.Accounts(ctx)
	if e != nil || accounts[0].ModelCount != 0 || accounts[1].ModelCount != 2 || accounts[0].ModelsSyncedAt == nil {
		t.Fatal("account counts incorrect")
	}
	if _, e = s.AccountModelsPage(ctx, 99999, 1, 20, ""); e != ErrNotFound {
		t.Fatal("missing account accepted")
	}
}

func TestAccountCatalogMigrationPreservesLegacyMembership(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "legacy.db")
	s, e := Open(ctx, path)
	if e != nil {
		t.Fatal(e)
	}
	actor, e := s.CreateAdmin(ctx, "admin", "hash")
	if e != nil {
		t.Fatal(e)
	}
	id, e := s.CreateAccount(ctx, actor, "legacy", "openai", func(int64) ([]byte, error) { return []byte("legacy-ciphertext"), nil })
	if e != nil {
		t.Fatal(e)
	}
	if e = s.SyncModels(ctx, id, "openai", []string{"legacy-model"}); e != nil {
		t.Fatal(e)
	}
	removeContributionSchema(t, s)
	// Recreate the exact pre-007 shape. The added column is the only schema delta.
	if _, e = s.DB.Exec("ALTER TABLE upstream_accounts DROP COLUMN models_synced_at"); e != nil {
		t.Fatal(e)
	}
	for _, q := range []string{"ALTER TABLE models DROP COLUMN protocols", "DROP TABLE request_attempts", "DROP TABLE account_cooldowns", "DROP TABLE account_health", "DROP TABLE account_policies", "ALTER TABLE upstream_accounts DROP COLUMN deleted_at"} {
		if _, e = s.DB.Exec(q); e != nil {
			t.Fatal(e)
		}
	}
	if _, e = s.DB.Exec("DELETE FROM schema_migrations WHERE version>=7"); e != nil {
		t.Fatal(e)
	}
	s.Close()
	s, e = Open(ctx, path)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	p, e := s.AccountModelsPage(ctx, id, 1, 20, "")
	if e != nil || p.Total != 1 || p.Items[0].ID != "legacy-model" || p.SyncedAt != nil {
		t.Fatal("legacy membership changed or a timestamp was invented")
	}
	account, e := s.Account(ctx, id)
	if e != nil || string(account.Credentials) != "legacy-ciphertext" {
		t.Fatal("migration altered credentials")
	}
}
