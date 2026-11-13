package store

import (
	"context"
	"path/filepath"
	"sync"
	"testing"

	"github.com/google/uuid"
)

func TestOpenAIDeviceSurvivesCredentialReplacementAndReopen(t *testing.T) {
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
	seal := func(int64) ([]byte, error) { return []byte("fixture"), nil }
	a, err := s.CreateAccount(ctx, actor, "a", "openai", seal)
	if err != nil {
		t.Fatal(err)
	}
	b, err := s.CreateAccount(ctx, actor, "b", "openai", seal)
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	ids := make(chan string, 8)
	for range 8 {
		wg.Go(func() {
			id, e := s.OpenAIDeviceID(ctx, a)
			if e != nil {
				t.Error(e)
				return
			}
			ids <- id
		})
	}
	wg.Wait()
	close(ids)
	device := ""
	for id := range ids {
		if device != "" && device != id {
			t.Fatal("concurrent access changed identity")
		}
		device = id
	}
	if _, err = uuid.Parse(device); err != nil {
		t.Fatal("device must be a random UUID", err)
	}
	other, err := s.OpenAIDeviceID(ctx, b)
	if err != nil || other == device {
		t.Fatal("accounts share a device", err)
	}
	if _, err = s.DB.Exec("UPDATE upstream_accounts SET credentials=? WHERE id=?", []byte("replacement-token"), a); err != nil {
		t.Fatal(err)
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	after, err := s.OpenAIDeviceID(ctx, a)
	if err != nil || after != device {
		t.Fatal("credential replacement/reopen changed device", err)
	}
}
