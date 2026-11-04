package accounts

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"testing"

	"fiftyapi/internal/credentials"
	"fiftyapi/internal/providers"
	"fiftyapi/internal/store"
)

func TestFiftyAPIOpensPersistedMasterAndAccountCiphertext(t *testing.T) {
	ctx := context.Background()
	s, err := store.Open(ctx, filepath.Join(t.TempDir(), "persisted.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	key := make([]byte, 32)
	check, err := credentials.Encrypt(key, "fiftyapi:master-key-check:v1", []byte("fiftyapi-key-verification"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.DB.Exec("INSERT INTO system_metadata(key,value) VALUES('master_key_check',?)", check); err != nil {
		t.Fatal(err)
	}
	admin, err := s.CreateAdmin(ctx, "admin", "hash")
	if err != nil {
		t.Fatal(err)
	}
	old := Credential{APIKey: "persisted-upstream-fixture", Config: providers.Config{Provider: "gateway", Gateway: &providers.GatewayConfig{Platform: "fiftyapi", BaseURL: "https://persisted.example/v1", Protocols: []string{"messages"}, Discovery: "manual", AuthMode: "bearer", Models: []providers.GatewayModel{{UpstreamID: "persisted-model", Protocol: "messages"}}}}}
	plain, _ := json.Marshal(old)
	id, err := s.CreateAccount(ctx, admin, "persisted", "gateway", func(id int64) ([]byte, error) {
		return credentials.Encrypt(key, fmt.Sprintf("fiftyapi:upstream:%d", id), plain)
	}, "fiftyapi")
	if err != nil {
		t.Fatal(err)
	}
	service, err := New(s, key)
	if err != nil {
		t.Fatal("persisted master key verification failed", err)
	}
	decoded, err := service.Access(ctx, id)
	if err != nil || decoded.APIKey != old.APIKey {
		t.Fatal("persisted encrypted account inaccessible", err)
	}
	if err = decoded.Config.Validate(); err != nil {
		t.Fatal("persisted upstream preset rejected", err)
	}
}
