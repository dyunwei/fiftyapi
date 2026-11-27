package store

import (
	"context"
	"path/filepath"
	"slices"
	"testing"

	"fiftyapi/internal/modelref"
)

func TestAccessGroupsLimitMemberCatalogProtocols(t *testing.T) {
	ctx := context.Background()
	s, err := Open(ctx, filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	actor, err := s.CreateAdmin(ctx, "admin", "hash")
	if err != nil {
		t.Fatal(err)
	}
	addAccount := func(protocol string) int64 {
		t.Helper()
		id, err := s.CreateAccount(ctx, actor, protocol, "gateway", func(int64) ([]byte, error) {
			return []byte("fixture-ciphertext"), nil
		})
		if err != nil {
			t.Fatal(err)
		}
		if _, err = s.SyncGatewayModels(ctx, id, []modelref.Mapping{{ID: "model", UpstreamID: "upstream", Protocol: protocol}}); err != nil {
			t.Fatal(err)
		}
		if err = s.AccountState(ctx, id, "catalog_ready", ""); err != nil {
			t.Fatal(err)
		}
		return id
	}
	addAccount("chat")
	restricted := addAccount("responses")
	if err = s.SetModel(ctx, actor, "model", true, &Price{Input: 100, Output: 100}); err != nil {
		t.Fatal(err)
	}
	group, err := s.SetAccessGroup(ctx, actor, 0, "Engineering")
	if err != nil {
		t.Fatal(err)
	}
	if err = s.SetAccountAccess(ctx, actor, restricted, AccountAccess{Mode: "groups", GroupIDs: []int64{group.ID}}); err != nil {
		t.Fatal(err)
	}
	check := func(wantProtocols []string, wantResponses int) {
		t.Helper()
		models, err := s.UserModels(ctx, actor)
		if err != nil || len(models) != 1 || !slices.Equal(models[0].Protocols, wantProtocols) {
			t.Fatalf("member catalog: %+v, %v", models, err)
		}
		page, err := s.ModelPage(ctx, 1, 10, "", true, ModelFilter{UserID: actor, Protocol: "responses"})
		if err != nil || page.Total != wantResponses {
			t.Fatalf("protocol-filtered member catalog: %+v, %v", page, err)
		}
		if wantResponses > 0 && (len(page.Items) != 1 || !slices.Equal(page.Items[0].Protocols, wantProtocols)) {
			t.Fatalf("paged protocols: %+v", page.Items)
		}
	}
	check([]string{"chat"}, 0)
	if err = s.SetUserAccess(ctx, actor, actor, UserAccess{GroupID: &group.ID}); err != nil {
		t.Fatal(err)
	}
	if err = s.SetUserAccess(ctx, actor, actor, UserAccess{Concurrency: 3}); err != nil {
		t.Fatal(err)
	}
	check([]string{"chat", "responses"}, 1)

	if err = s.SetAccountAccess(ctx, actor, restricted, AccountAccess{Mode: "groups", GroupIDs: []int64{0}}); err == nil {
		t.Fatal("nonexistent group accepted")
	}
	check([]string{"chat", "responses"}, 1)

	if err = s.DeleteAccessGroup(ctx, actor, group.ID); err != nil {
		t.Fatal(err)
	}
	memberAccess, err := s.UserAccess(ctx, actor)
	if err != nil || memberAccess.GroupID != nil || memberAccess.Concurrency != 3 {
		t.Fatalf("member access after group removal: %+v, %v", memberAccess, err)
	}
	access, err := s.AccountAccess(ctx, restricted)
	if err != nil || access.Mode != "groups" || len(access.GroupIDs) != 0 {
		t.Fatalf("account access after group removal: %+v, %v", access, err)
	}
	check([]string{"chat"}, 0)
}
