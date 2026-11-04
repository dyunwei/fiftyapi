package store

import (
	"context"
	"path/filepath"
	"testing"
)

func TestServerLockExclusiveAndReleased(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "app.db")
	a, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	b, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	unlock, err := a.LockServer()
	if err != nil {
		t.Fatal(err)
	}
	if release, err := b.LockServer(); err == nil {
		release()
		unlock()
		t.Fatal("second serving instance acquired lock")
	}
	unlock()
	release, err := b.LockServer()
	if err != nil {
		t.Fatal(err)
	}
	release()
}

func TestLogCursorDoesNotSkipSameSecond(t *testing.T) {
	ctx := context.Background()
	s, err := Open(ctx, filepath.Join(t.TempDir(), "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	u, err := s.CreateAdmin(ctx, "admin", "hash")
	if err != nil {
		t.Fatal(err)
	}
	key, _, err := s.CreateAPIKey(ctx, u, "test")
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"a", "b", "c"} {
		if err = s.LogRequest(ctx, RequestLog{ID: id, UserID: u, KeyID: key.ID, Model: "test", Provider: "claude", Status: 200, CreatedAt: 100}); err != nil {
			t.Fatal(err)
		}
	}
	rows, err := s.LogsPage(ctx, u, false, 100, "", 0, "b", 0)
	if err != nil || len(rows) != 1 || rows[0].ID != "a" {
		t.Fatalf("same-second cursor: %v %v", rows, err)
	}
}

func TestRequestIDSearchKeepsUserScope(t *testing.T) {
	ctx := context.Background()
	s, err := Open(ctx, filepath.Join(t.TempDir(), "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	admin, err := s.CreateAdmin(ctx, "admin", "hash")
	if err != nil {
		t.Fatal(err)
	}
	other, err := s.CreateUser(ctx, admin, "member", "hash", 100, 100)
	if err != nil {
		t.Fatal(err)
	}
	key, _, err := s.CreateAPIKey(ctx, admin, "test")
	if err != nil {
		t.Fatal(err)
	}
	if err = s.LogRequest(ctx, RequestLog{ID: "private-request", UserID: admin, KeyID: key.ID, Model: "model", Provider: "antigravity", Status: 200, CreatedAt: 1}); err != nil {
		t.Fatal(err)
	}
	rows, err := s.LogsPage(ctx, other, false, 0, "", 0, "", 0, "private-request")
	if err != nil || len(rows) != 0 {
		t.Fatal("request ID bypassed ownership")
	}
	rows, err = s.LogsPage(ctx, admin, true, 0, "", 0, "", 0, "private-request")
	if err != nil || len(rows) != 1 {
		t.Fatal("admin request ID search failed")
	}
}
