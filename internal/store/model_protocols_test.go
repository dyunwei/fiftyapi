package store

import (
	"context"
	"fiftyapi/internal/modelref"
	"path/filepath"
	"testing"
)

func TestMultiProtocolSyncPreservesLegacyIDsAndPrices(t *testing.T) {
	ctx := context.Background()
	s, e := Open(ctx, filepath.Join(t.TempDir(), "db"))
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	actor, _ := s.CreateAdmin(ctx, "admin", "hash")
	account, e := s.CreateAccount(ctx, actor, "gateway", "gateway", func(int64) ([]byte, error) { return []byte("ciphertext"), nil })
	if e != nil {
		t.Fatal(e)
	}
	old := []modelref.Mapping{{ID: "old-messages", UpstreamID: "upstream", Protocol: "messages"}, {ID: "old-responses", UpstreamID: "upstream", Protocol: "responses"}}
	if _, e = s.SyncGatewayModels(ctx, account, old); e != nil {
		t.Fatal(e)
	}
	if e = s.SetModel(ctx, actor, "old-messages", true, &Price{Input: 1, Output: 2}); e != nil {
		t.Fatal(e)
	}
	if e = s.SetModel(ctx, actor, "old-responses", true, &Price{Input: 3, Output: 4}); e != nil {
		t.Fatal(e)
	}
	_ = s.AccountState(ctx, account, "catalog_ready", "")
	ids, e := s.SyncGatewayModels(ctx, account, []modelref.Mapping{{UpstreamID: "upstream", Protocols: []string{"messages", "responses", "chat"}}})
	if e != nil || len(ids) != 2 {
		t.Fatal("legacy aliases lost", e)
	}
	canonical, _, e := s.RoutableModel(ctx, ids[0])
	if e != nil || len(canonical.Protocols) != 3 {
		t.Fatal("canonical model missing capabilities")
	}

	if _, e = s.DB.Exec("INSERT INTO account_cooldowns(account_id,scope,until_at,reason) VALUES(?,?,unixepoch()+60,'chat denied')", account, "generate/chat:"+ids[0]); e != nil {
		t.Fatal(e)
	}
	page, e := s.AccountModelsPage(ctx, account, 1, 20, "")
	if e != nil {
		t.Fatal(e)
	}
	for _, m := range page.Items {
		if m.ID == ids[0] {
			if !m.Routable || len(m.ProtocolStatus) != 3 {
				t.Fatal("one protocol cooldown blocked all protocols")
			}
			for _, p := range m.ProtocolStatus {
				if p.Protocol == "chat" && p.Routable {
					t.Fatal("cooled protocol advertised available")
				}
			}
		}
	}
	chat, e := s.RouteCandidates(ctx, ids[0], "generate/chat:"+ids[0], "chat")
	if e != nil || len(chat) != 1 || chat[0].CooldownUntil == 0 {
		t.Fatal("chat cooldown missing")
	}
	responses, e := s.RouteCandidates(ctx, ids[0], "generate/responses:"+ids[0], "responses")
	if e != nil || len(responses) != 1 || responses[0].CooldownUntil != 0 {
		t.Fatal("chat cooldown leaked into responses")
	}
	for _, id := range []string{"old-messages", "old-responses"} {
		m, _, e := s.RoutableModel(ctx, id)
		if e != nil || m.Price == nil {
			t.Fatal("legacy priced ID no longer routable")
		}
		if id == "old-messages" && m.Price.Input != 1 || id == "old-responses" && m.Price.Input != 3 {
			t.Fatal("old price changed")
		}
	}
	if _, e = s.SyncGatewayModels(ctx, account, []modelref.Mapping{{UpstreamID: "upstream", Protocols: []string{"chat"}}}); e != nil {
		t.Fatal(e)
	}
	candidates, e := s.RouteCandidates(ctx, ids[0], "generate/chat:"+ids[0], "chat")
	if e != nil || len(candidates) != 1 {
		t.Fatal("retained protocol lost route")
	}
	candidates, e = s.RouteCandidates(ctx, ids[0], "generate/responses:"+ids[0], "responses")
	if e != nil || len(candidates) != 0 {
		t.Fatal("removed protocol remained routable")
	}
}
func TestProtocolMigrationPreservesExistingModels(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "db")
	s, e := Open(ctx, path)
	if e != nil {
		t.Fatal(e)
	}
	actor, _ := s.CreateAdmin(ctx, "admin", "hash")
	account, _ := s.CreateAccount(ctx, actor, "gateway", "gateway", func(int64) ([]byte, error) { return []byte("ciphertext"), nil })
	_, e = s.SyncGatewayModels(ctx, account, []modelref.Mapping{{ID: "legacy", UpstreamID: "remote", Protocol: "responses"}})
	if e != nil {
		t.Fatal(e)
	}
	_ = s.SetModel(ctx, actor, "legacy", true, &Price{Input: 7, Output: 9})
	_ = s.AccountState(ctx, account, "catalog_ready", "")
	removeContributionSchema(t, s)
	if _, e = s.DB.Exec("ALTER TABLE models DROP COLUMN protocols"); e != nil {
		t.Fatal(e)
	}
	if _, e = s.DB.Exec("DELETE FROM schema_migrations WHERE version=9"); e != nil {
		t.Fatal(e)
	}
	s.Close()
	s, e = Open(ctx, path)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	m, ids, e := s.RoutableModel(ctx, "legacy")
	if e != nil || len(ids) != 1 || !m.SupportsProtocol("responses") || m.SupportsProtocol("chat") || m.Price.Input != 7 {
		t.Fatal("schema 8 upgrade changed ID, protocol or price")
	}
}

func TestDisjointProtocolAliasesSurviveRepeatedSync(t *testing.T) {
	ctx := context.Background()
	s, e := Open(ctx, filepath.Join(t.TempDir(), "db"))
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	actor, _ := s.CreateAdmin(ctx, "admin", "hash")
	id, _ := s.CreateAccount(ctx, actor, "gateway", "gateway", func(int64) ([]byte, error) { return []byte("cipher"), nil })
	mappings := []modelref.Mapping{{ID: "chat-and-messages", UpstreamID: "same-upstream", Protocols: []string{"chat", "messages"}}, {ID: "responses-only", UpstreamID: "same-upstream", Protocols: []string{"responses"}}}
	for i := 0; i < 2; i++ {
		ids, e := s.SyncGatewayModels(ctx, id, mappings)
		if e != nil || len(ids) != 2 {
			t.Fatal("disjoint protocol aliases collided", e)
		}
		page, e := s.AccountModelsPage(ctx, id, 1, 20, "")
		if e != nil || page.Total != 2 {
			t.Fatal("mapping count changed")
		}
	}
}
