package credentials

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestEncryptedFileLifecycle(t *testing.T) {
	dir := t.TempDir()
	keyPath := filepath.Join(dir, "master.key")
	path := filepath.Join(dir, "account.session")
	key, err := LoadKey(keyPath)
	if err != nil {
		t.Fatal(err)
	}
	again, err := LoadKey(keyPath)
	if err != nil || !bytes.Equal(key, again) {
		t.Fatal("key changed")
	}
	plain := []byte(`{"access_token":"private-access","refresh_token":"private-refresh"}`)
	if err = Save(path, key, "provider-and-endpoint", plain); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(raw, []byte("private")) {
		t.Fatal("plaintext credential persisted")
	}
	decoded, err := Load(path, key, "provider-and-endpoint")
	if err != nil || !bytes.Equal(decoded, plain) {
		t.Fatalf("roundtrip failed: %v", err)
	}
	if _, err = Load(path, key, "another-provider"); err == nil {
		t.Fatal("accepted wrong binding")
	}
	wrong := append([]byte(nil), key...)
	wrong[0] ^= 1
	if _, err = Load(path, wrong, "provider-and-endpoint"); err == nil {
		t.Fatal("accepted wrong key")
	}
	raw[len(raw)-1] ^= 1
	os.WriteFile(path, raw, 0600)
	if _, err = Load(path, key, "provider-and-endpoint"); err == nil {
		t.Fatal("accepted tampered ciphertext")
	}
	if err = Save(path, key, "provider-and-endpoint", []byte("updated")); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{path, keyPath} {
		info, _ := os.Stat(p)
		if info.Mode().Perm() != 0600 {
			t.Fatal("unsafe permissions")
		}
	}
}

func TestInvalidExistingKeyNotOverwritten(t *testing.T) {
	path := filepath.Join(t.TempDir(), "key")
	os.WriteFile(path, []byte("invalid"), 0600)
	if _, err := LoadKey(path); err == nil {
		t.Fatal("accepted invalid key")
	}
	raw, _ := os.ReadFile(path)
	if string(raw) != "invalid" {
		t.Fatal("overwrote existing key")
	}
}

func TestBadEnvelope(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session")
	key := make([]byte, 32)
	for _, b := range [][]byte{nil, []byte("ZTM1"), bytes.Repeat([]byte("x"), maxSize+128)} {
		os.WriteFile(path, b, 0600)
		if _, err := Load(path, key, "provider"); err == nil {
			t.Fatal("accepted invalid envelope")
		}
	}
}
