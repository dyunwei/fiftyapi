package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"fiftyapi/internal/providers"
)

func TestSessionResumeAndBinding(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "session")
	key := filepath.Join(dir, "key")
	c := providers.Config{Provider: "antigravity", ClientID: "client", TokenURL: "https://auth.example/token", ModelsURL: "https://api.example/models"}
	store, saved, err := openSession(path, key, c)
	if err != nil || saved != nil {
		t.Fatalf("open new: %v", err)
	}
	expires := time.Now().Add(time.Hour).UTC().Truncate(time.Second)
	if err = store.save(providers.Token{AccessToken: "access", RefreshToken: "refresh", ExpiresIn: 3600}, expires); err != nil {
		t.Fatal(err)
	}
	_, saved, err = openSession(path, key, c)
	if err != nil || saved == nil || saved.Token.RefreshToken != "refresh" || !saved.ExpiresAt.Equal(expires) {
		t.Fatalf("resume: %v", err)
	}
	c.ModelsURL = "https://different.example/models"
	if _, _, err = openSession(path, key, c); err == nil {
		t.Fatal("accepted changed destination")
	}
	os.Remove(key)
	if _, _, err = openSession(path, key, c); err == nil {
		t.Fatal("created replacement key for existing credentials")
	}
	if _, err = os.Stat(key); !os.IsNotExist(err) {
		t.Fatal("key unexpectedly regenerated")
	}
}

func TestSessionPaths(t *testing.T) {
	c := providers.Config{}
	if _, _, err := openSession("file", "", c); err == nil {
		t.Fatal("missing key accepted")
	}
	path := filepath.Join(t.TempDir(), "file")
	if _, _, err := openSession(path, filepath.Join(filepath.Dir(path), ".", "file"), c); err == nil {
		t.Fatal("same paths accepted")
	}
	store, saved, err := openSession("", "", c)
	if err != nil || store != nil || saved != nil {
		t.Fatal("memory mode failed")
	}
	if err = store.save(providers.Token{}, time.Now()); err != nil {
		t.Fatal(err)
	}
}
