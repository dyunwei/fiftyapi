package store

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"
	"time"
)

func TestDashboardScopesAndRealLogAggregation(t *testing.T) {
	ctx := context.Background()
	s, e := Open(ctx, filepath.Join(t.TempDir(), "db"))
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	admin, _ := s.CreateAdmin(ctx, "admin", "hash")
	alice, _ := s.CreateUser(ctx, admin, "alice", "hash", 100, 1000)
	key1, _, _ := s.CreateAPIKey(ctx, admin, "admin")
	key2, _, _ := s.CreateAPIKey(ctx, alice, "alice")
	input, output, amount := int64(10), int64(2), int64(12)
	for _, who := range []struct{ user, key int64 }{{admin, key1.ID}, {alice, key2.ID}} {
		id := "admin"
		if who.user == alice {
			id = "alice"
		}
		if e = s.LogRequest(ctx, RequestLog{ID: id, UserID: who.user, KeyID: who.key, Model: "model", Provider: "claude", Status: 200, Input: &input, Output: &output, Amount: &amount, DurationMS: 500, CreatedAt: time.Now().Unix()}); e != nil {
			t.Fatal(e)
		}
	}
	own, e := s.Dashboard(ctx, alice, false, 24, 3600, 480)
	if e != nil {
		t.Fatal(e)
	}
	if own.TotalRequests != 1 || own.TotalTokens != 12 || own.Keys != 1 || len(own.Distribution) != 1 {
		t.Fatalf("user stats exposed other data: %+v", own)
	}
	all, e := s.Dashboard(ctx, admin, true, 24, 3600, 480)
	if e != nil {
		t.Fatal(e)
	}
	if all.TotalRequests != 2 || all.TotalTokens != 24 || all.TodayRequests != 2 || all.Users != 2 || all.ActiveUsers != 2 || len(all.Trend) != 1 {
		t.Fatalf("wrong actual aggregation: %+v", all)
	}
}

func TestDashboardErrorsScopeAndTimeRange(t *testing.T) {
	s, admin, key := quotaStore(t)
	ctx := context.Background()
	other, err := s.CreateUser(ctx, admin, "error-user", "hash", 100, 1000)
	if err != nil {
		t.Fatal(err)
	}
	k, _, err := s.CreateAPIKey(ctx, other, "error-key")
	if err != nil {
		t.Fatal(err)
	}
	for i, v := range []struct {
		user, key int64
		status    int
		age       int64
	}{{admin, key, 200, 0}, {admin, key, 429, 0}, {admin, key, 502, 0}, {other, k.ID, 502, 0}, {admin, key, 503, 48 * 3600}} {
		if err = s.LogRequest(ctx, RequestLog{ID: fmt.Sprintf("error-%d", i), UserID: v.user, KeyID: v.key, Model: "test", Provider: "claude", Status: v.status, CreatedAt: time.Now().Unix() - v.age}); err != nil {
			t.Fatal(err)
		}
	}
	own, err := s.Dashboard(ctx, other, false, 24, 3600, 480)
	if err != nil || len(own.Errors) != 1 || own.Errors[0].Status != 502 || own.Errors[0].Requests != 1 {
		t.Fatal("scoped errors", err, own.Errors)
	}
	all, err := s.Dashboard(ctx, admin, true, 24, 3600, 480)
	if err != nil || len(all.Errors) != 2 || all.Errors[0].Status != 502 || all.Errors[0].Requests != 2 || all.Errors[1].Status != 429 {
		t.Fatal("time range/status aggregation", err, all.Errors)
	}
}
