package store

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"
	"time"
)

func TestOperationalIncidentTransitions(t *testing.T) {
	ctx := context.Background()
	s, err := Open(ctx, filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	user, err := s.CreateAdmin(ctx, "admin", "hash")
	if err != nil {
		t.Fatal(err)
	}
	key, _, err := s.CreateAPIKey(ctx, user, "fixture")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().Truncate(time.Second)
	add := func(count, status int, at time.Time, code string) {
		t.Helper()
		for i := 0; i < count; i++ {
			err := s.LogRequest(ctx, RequestLog{ID: fmt.Sprintf("%d-%d", at.Unix(), i), UserID: user, KeyID: key.ID, Model: "fixture", Status: status, CreatedAt: at.Unix(), Diagnostics: &RequestDiagnostics{Protocol: "responses", Code: code, Completed: status == 200}})
			if err != nil {
				t.Fatal(err)
			}
		}
	}
	check := func(at time.Time, count int) []IncidentTransition {
		t.Helper()
		changes, err := s.IncidentTransitions(ctx, at)
		if err != nil || len(changes) != count {
			t.Fatalf("got %+v, want %d: %v", changes, count, err)
		}
		return changes
	}
	add(3, 503, now.Add(-6*time.Minute), "route_unavailable")
	check(now, 0)
	add(3, 503, now.Add(-time.Minute), "route_unavailable")
	opened := check(now, 1)
	if opened[0].Recovered {
		t.Fatal("unexpected recovery")
	}
	if err = s.RecordIncidentTransitions(ctx, opened); err != nil {
		t.Fatal(err)
	}
	check(now, 0)
	check(now.Add(20*time.Minute), 0)
	add(3, 200, now.Add(19*time.Minute), "")
	recovered := check(now.Add(20*time.Minute), 1)
	if !recovered[0].Recovered {
		t.Fatal("no recovery")
	}
	if err = s.RecordIncidentTransitions(ctx, recovered); err != nil {
		t.Fatal(err)
	}
	check(now.Add(20*time.Minute), 0)
}
