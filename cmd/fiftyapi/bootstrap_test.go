package main

import (
	"bytes"
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAutomaticSecretsPersistAndDifferBetweenInstallations(t *testing.T) {
	t.Setenv("FIFTYAPI_MASTER_KEY", "")
	t.Setenv("FIFTYAPI_SETUP_TOKEN", "")
	path := filepath.Join(t.TempDir(), "app.db")
	key, err := masterKey(path, true)
	if err != nil || len(key) != 32 {
		t.Fatal("generate key", err)
	}
	token, err := initializationToken(path)
	if err != nil || len(token) != 64 {
		t.Fatal("generate token", err)
	}
	if err = os.WriteFile(path, []byte("existing database"), 0600); err != nil {
		t.Fatal(err)
	}
	again, err := masterKey(path, true)
	if err != nil || !bytes.Equal(key, again) {
		t.Fatal("key changed after restart", err)
	}
	repeated, err := initializationToken(path)
	if err != nil || repeated != token {
		t.Fatal("token changed after restart", err)
	}
	for _, name := range []string{"master.key", "setup-token"} {
		info, err := os.Stat(filepath.Join(filepath.Dir(path), name))
		if err != nil || info.Mode().Perm() != 0600 {
			t.Fatal("unsafe secret permissions", err)
		}
	}
	other := filepath.Join(t.TempDir(), "app.db")
	otherKey, err := masterKey(other, true)
	if err != nil || bytes.Equal(key, otherKey) {
		t.Fatal("installations share a key", err)
	}
	otherToken, err := initializationToken(other)
	if err != nil || otherToken == token {
		t.Fatal("installations share a setup token", err)
	}
}

func TestLostOrInvalidMasterKeyIsNeverReplaced(t *testing.T) {
	t.Setenv("FIFTYAPI_MASTER_KEY", "")
	path := filepath.Join(t.TempDir(), "app.db")
	if err := os.WriteFile(path, []byte("existing database"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := masterKey(path, true); err == nil {
		t.Fatal("generated a new key for existing database")
	}
	keyPath := filepath.Join(filepath.Dir(path), "master.key")
	if _, err := os.Stat(keyPath); !os.IsNotExist(err) {
		t.Fatal("replacement key written")
	}
	for _, content := range []string{"", "fiftyapi"} {
		if err := os.WriteFile(keyPath, []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := masterKey(path, true); err == nil {
			t.Fatal("accepted invalid key")
		}
		saved, _ := os.ReadFile(keyPath)
		if string(saved) != content {
			t.Fatal("invalid key overwritten")
		}
	}
	if err := os.Remove(keyPath); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(t.TempDir(), "external-key")
	if err := os.WriteFile(target, []byte(base64.StdEncoding.EncodeToString(make([]byte, 32))), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, keyPath); err != nil {
		t.Fatal(err)
	}
	if _, err := masterKey(path, true); err == nil {
		t.Fatal("followed secret symlink")
	}
}

func TestExplicitSecretsTakePrecedenceAndValidate(t *testing.T) {
	path := filepath.Join(t.TempDir(), "app.db")
	key := bytes.Repeat([]byte{1}, 32)
	t.Setenv("FIFTYAPI_MASTER_KEY", base64.StdEncoding.EncodeToString(key))
	t.Setenv("FIFTYAPI_SETUP_TOKEN", strings.Repeat("s", 32))
	got, err := masterKey(path, true)
	if err != nil || !bytes.Equal(got, key) {
		t.Fatal("explicit key ignored", err)
	}
	if token, err := initializationToken(path); err != nil || token != strings.Repeat("s", 32) {
		t.Fatal("explicit token ignored", err)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(path), "master.key")); !os.IsNotExist(err) {
		t.Fatal("explicit key copied into volume")
	}
	t.Setenv("FIFTYAPI_MASTER_KEY", "fiftyapi")
	if _, err := masterKey(path, true); err == nil {
		t.Fatal("accepted invalid explicit key")
	}
	t.Setenv("FIFTYAPI_SETUP_TOKEN", "fiftyapi")
	if _, err := initializationToken(path); err == nil {
		t.Fatal("accepted short explicit token")
	}
	t.Setenv("FIFTYAPI_MASTER_KEY", "")
	if _, err := masterKey(path, false); err == nil {
		t.Fatal("import generated a key")
	}
}
