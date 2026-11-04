package store

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestCopyableAPIKeysEncryptedOwnedAndRevocable(t *testing.T) {
	s, user, _ := quotaStore(t)
	ctx := context.Background()
	master := bytes.Repeat([]byte{7}, 32)
	key, token, err := s.CreateAPIKey(ctx, user, "copyable", master)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(token, "sk-") || len(token) != 46 || key.Suffix != token[len(token)-4:] || !key.Copyable {
		t.Fatal("invalid new key format")
	}
	if _, _, err = s.AuthenticateAPIKey(ctx, token); err != nil {
		t.Fatal(err)
	}
	var encrypted []byte
	if err = s.DB.QueryRow("SELECT token_encrypted FROM api_keys WHERE id=?", key.ID).Scan(&encrypted); err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(encrypted, []byte(token)) {
		t.Fatal("plaintext key stored")
	}
	got, err := s.RevealAPIKey(ctx, user, key.ID, master)
	if err != nil || got != token {
		t.Fatal("cannot decrypt own key", err)
	}
	if _, err = s.RevealAPIKey(ctx, user+1, key.ID, master); err == nil {
		t.Fatal("other user read key")
	}
	if _, err = s.RevealAPIKey(ctx, user, key.ID, make([]byte, 32)); err == nil {
		t.Fatal("wrong master key accepted")
	}
	list, err := s.APIKeys(ctx, user)
	if err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(list)
	if bytes.Contains(data, []byte(token)) || bytes.Contains(data, []byte("token_encrypted")) {
		t.Fatal("list exposes secret")
	}
	// Copying a ciphertext into another row must fail its authenticated binding.
	other, _, err := s.CreateAPIKey(ctx, user, "other", master)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.DB.Exec("UPDATE api_keys SET token_encrypted=? WHERE id=?", encrypted, other.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.RevealAPIKey(ctx, user, other.ID, master); err == nil {
		t.Fatal("swapped ciphertext accepted")
	}
	if err = s.RevokeAPIKey(ctx, user, key.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.RevealAPIKey(ctx, user, key.ID, master); err == nil {
		t.Fatal("revoked key copied")
	}
	if _, _, err = s.AuthenticateAPIKey(ctx, token); err == nil {
		t.Fatal("revoked key authenticated")
	}
	if err = s.DeleteAPIKey(ctx, user, other.ID); err != nil {
		t.Fatal(err)
	}
	if err = s.DB.QueryRow("SELECT token_encrypted FROM api_keys WHERE id=?", other.ID).Scan(&encrypted); err != nil || len(encrypted) != 0 {
		t.Fatal("deleted ciphertext retained", err)
	}
}
