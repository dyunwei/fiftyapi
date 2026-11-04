package store

import (
	"context"
	"errors"
	"io/fs"
	"path/filepath"
	"sync"
	"testing"
)

func TestConcurrentSetupAndReopen(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "db.sqlite")
	s, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	results := make(chan error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); _, err := s.CreateAdmin(ctx, "admin", "hash"); results <- err }()
	}
	wg.Wait()
	close(results)
	successes := 0
	for err := range results {
		if err == nil {
			successes++
		} else if !errors.Is(err, ErrInitialized) {
			t.Fatal(err)
		}
	}
	if successes != 1 {
		t.Fatalf("created %d admins", successes)
	}
	var mode string
	if err = s.DB.QueryRow("PRAGMA journal_mode").Scan(&mode); err != nil || mode != "wal" {
		t.Fatalf("WAL not enabled: %v", err)
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	users, err := s.Users(ctx)
	if err != nil || len(users) != 1 {
		t.Fatalf("migration or persistence failed: %v", err)
	}
	var n int
	s.DB.QueryRow("SELECT count(*) FROM schema_migrations").Scan(&n)
	files, _ := fs.Glob(migrations, "migrations/*.sql")
	if n != len(files) {
		t.Fatal("migration repeated")
	}
}

func TestWrongMasterKeyRejected(t *testing.T) {
	s, err := Open(context.Background(), filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	key := make([]byte, 32)
	if err = s.CheckMasterKey(context.Background(), key); err != nil {
		t.Fatal(err)
	}
	key[0] = 1
	if err = s.CheckMasterKey(context.Background(), key); err == nil {
		t.Fatal("wrong master key accepted")
	}
	key[0] = 0
	if err = s.CheckMasterKey(context.Background(), key); err != nil {
		t.Fatal("valid master key overwritten")
	}
}
