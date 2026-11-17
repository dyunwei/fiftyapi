package store

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

func TestUserModelAccessAndAtomicConcurrency(t *testing.T) {
	s, user, key := quotaStore(t)
	ctx := context.Background()
	if err := s.SetUserAccess(ctx, user, user, UserAccess{Models: []string{"allowed"}, Concurrency: 2}); err != nil {
		t.Fatal(err)
	}
	if err := s.Reserve(ctx, "denied", user, key, "other", Price{}, 1, time.Now()); !errors.Is(err, ErrModelAccess) {
		t.Fatal("model permission bypass", err)
	}
	var wg sync.WaitGroup
	results := make(chan error, 20)
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			results <- s.Reserve(ctx, string(rune('a'+i)), user, key, "allowed", Price{}, 1, time.Now())
		}(i)
	}
	wg.Wait()
	close(results)
	accepted := 0
	for err := range results {
		if err == nil {
			accepted++
		} else if !errors.Is(err, ErrUserConcurrency) {
			t.Fatal(err)
		}
	}
	if accepted != 2 {
		t.Fatalf("admitted %d concurrent requests", accepted)
	}
	if err := s.SetUserAccess(ctx, user, user, UserAccess{Models: []string{}}); err != nil {
		t.Fatal(err)
	}
	access, _ := s.UserAccess(ctx, user)
	if access.Allows("allowed") {
		t.Fatal("empty allowlist permits models")
	}
	if err := s.SetUserAccess(ctx, user, user, UserAccess{}); err != nil {
		t.Fatal(err)
	}
	access, _ = s.UserAccess(ctx, user)
	if !access.Allows("new-model") {
		t.Fatal("unrestricted policy does not include new models")
	}
}
