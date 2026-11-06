package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

// Legacy migration tests must remove the later schema before replaying it.
func removeContributionSchema(t *testing.T, s *Store) {
	t.Helper()
	for _, column := range []string{"reasoning_effort", "thinking_type", "thinking_budget_tokens", "speed", "service_tier"} {
		if _, err := s.DB.Exec("ALTER TABLE request_logs DROP COLUMN " + column); err != nil {
			t.Fatal(err)
		}
	}
	for _, q := range []string{"ALTER TABLE api_keys DROP COLUMN suffix", "ALTER TABLE api_keys DROP COLUMN token_encrypted", "DROP TABLE backup_records", "DROP TABLE runtime_logs", "DROP INDEX account_auth_fingerprint", "ALTER TABLE upstream_accounts DROP COLUMN auth_fingerprint", "ALTER TABLE upstream_accounts DROP COLUMN contribution_link_id", "DROP TABLE account_contribution_links", "DELETE FROM schema_migrations WHERE version>=10"} {
		if _, err := s.DB.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
}
func TestContributionAtomicDisabledAndRevocation(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "db")
	s, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { s.Close() }()
	actor, err := s.CreateAdmin(ctx, "admin", "hash")
	if err != nil {
		t.Fatal(err)
	}
	link, err := s.CreateContributionLink(ctx, actor, []byte("hash"), "openai", "team", time.Now().Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	origin := AccountOrigin{LinkID: link, Fingerprint: []byte("fingerprint")}
	seal := func(int64) ([]byte, error) { return []byte("ciphertext"), nil }
	id, err := s.CreateAccountWithOrigin(ctx, actor, "contributed", "openai", seal, "", origin)
	if err != nil {
		t.Fatal(err)
	}
	record, err := s.Account(ctx, id)
	if err != nil || record.Enabled {
		t.Fatal("contributed account must be disabled at creation")
	}
	if _, err = s.CreateAccountWithOrigin(ctx, actor, "wrong-provider", "claude", seal, "", origin); err == nil {
		t.Fatal("link provider bypass")
	}
	if _, err = s.CreateAccountWithOrigin(ctx, actor, "duplicate", "openai", seal, "", origin); err == nil {
		t.Fatal("duplicate credentials accepted")
	}
	rows, err := s.ContributionLinks(ctx)
	if err != nil || len(rows) != 1 || rows[0].Count != 1 {
		t.Fatalf("unexpected contributions: %v %v", rows, err)
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.ContributionLink(ctx, []byte("hash")); err != nil {
		t.Fatal("link lost after reopen", err)
	}
	if err = s.RevokeContributionLink(ctx, actor, link); err != nil {
		t.Fatal(err)
	}
	if _, err = s.ContributionLink(ctx, []byte("hash")); err == nil {
		t.Fatal("revoked link accepted")
	}
	origin.Fingerprint = []byte("different")
	if _, err = s.CreateAccountWithOrigin(ctx, actor, "after-revoke", "openai", seal, "", origin); err == nil {
		t.Fatal("revocation not checked in account transaction")
	}
	record, err = s.Account(ctx, id)
	if err != nil || record.Enabled {
		t.Fatal("revocation changed contribution")
	}
	if _, err = s.DB.Exec("UPDATE account_contribution_links SET revoked_at=NULL,expires_at=0 WHERE id=?", link); err != nil {
		t.Fatal(err)
	}
	if _, err = s.CreateAccountWithOrigin(ctx, actor, "expired", "openai", seal, "", origin); err == nil {
		t.Fatal("expired link accepted")
	}
	if err = s.EnableAccount(ctx, actor, id, true); err != nil {
		t.Fatal(err)
	}
	record, err = s.Account(ctx, id)
	if err != nil || !record.Enabled {
		t.Fatal("admin cannot enable contribution")
	}
}
