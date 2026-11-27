package store

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"testing"
	"time"

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
	group, err := s.SetAccessGroup(ctx, actor, 0, "Engineering", nil)
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
	if err != nil || memberAccess.GroupID != nil || memberAccess.Concurrency != 0 {
		t.Fatalf("member access after group removal: %+v, %v", memberAccess, err)
	}
	access, err := s.AccountAccess(ctx, restricted)
	if err != nil || access.Mode != "groups" || len(access.GroupIDs) != 0 {
		t.Fatalf("account access after group removal: %+v, %v", access, err)
	}
	check([]string{"chat"}, 0)
}

func TestGroupPolicyUpgradeAndPerMemberConcurrency(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "db")
	s, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	actor, err := s.CreateAdmin(ctx, "admin", "hash")
	if err != nil {
		t.Fatal(err)
	}
	member, err := s.CreateUser(ctx, actor, "member", "hash", 1000, 1000)
	if err != nil {
		t.Fatal(err)
	}
	group, err := s.SetAccessGroup(ctx, actor, 0, "Legacy", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.SetUserAccess(ctx, actor, actor, UserAccess{GroupID: &group.ID, Models: []string{"old"}, Concurrency: 2}); err != nil {
		t.Fatal(err)
	}
	_, err = s.DB.Exec(`ALTER TABLE access_groups DROP COLUMN models;
 ALTER TABLE access_groups DROP COLUMN concurrency;
 DELETE FROM schema_migrations WHERE version=26;`)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	groups, err := s.AccessGroups(ctx)
	if err != nil || len(groups) != 1 || groups[0].PolicyConfigured {
		t.Fatalf("legacy group policy activated during upgrade: %+v, %v", groups, err)
	}
	legacy, err := s.UserAccess(ctx, actor)
	if err != nil || legacy.Concurrency != 2 || legacy.Allows("new") || !legacy.Allows("old") {
		t.Fatalf("upgrade changed individual limits: %+v, %v", legacy, err)
	}

	if _, err = s.SetAccessGroup(ctx, actor, group.ID, group.Name, &AccessGroupPolicy{Models: []string{"new"}, Concurrency: 1}); err != nil {
		t.Fatal(err)
	}
	if err = s.SetUserGroup(ctx, actor, member, group.ID); err != nil {
		t.Fatal(err)
	}
	for _, user := range []int64{actor, member} {
		key, _, err := s.CreateAPIKey(ctx, user, "test")
		if err != nil {
			t.Fatal(err)
		}
		now := time.Now()
		if err = s.Reserve(ctx, fmt.Sprintf("old-%d", user), user, key.ID, "old", Price{}, 1, now); !errors.Is(err, ErrModelAccess) {
			t.Fatalf("legacy model access persisted for user %d: %v", user, err)
		}
		if err = s.Reserve(ctx, fmt.Sprintf("new-%d", user), user, key.ID, "new", Price{}, 1, now); err != nil {
			t.Fatalf("first concurrent request for user %d: %v", user, err)
		}
		if err = s.Reserve(ctx, fmt.Sprintf("extra-%d", user), user, key.ID, "new", Price{}, 1, now); !errors.Is(err, ErrUserConcurrency) {
			t.Fatalf("group concurrency limit not applied per member %d: %v", user, err)
		}
	}
	if err = s.SetUserGroup(ctx, actor, member, 0); err != nil {
		t.Fatal(err)
	}
	ungrouped, err := s.UserAccess(ctx, member)
	if err != nil || ungrouped.GroupID != nil || ungrouped.Concurrency != 0 || !ungrouped.Allows("old") {
		t.Fatalf("policy after removing member from group: %+v, %v", ungrouped, err)
	}
}
