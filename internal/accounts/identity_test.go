package accounts

import (
	"context"
	"encoding/json"
	"fiftyapi/internal/credentials"
	"fiftyapi/internal/providers"
	"fiftyapi/internal/store"
	"path/filepath"
	"testing"
	"time"
)

func TestGlobalIdentityInheritancePersistsAcrossRefreshSave(t *testing.T) {
	ctx := context.Background()
	s, err := store.Open(ctx, filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	actor, _ := s.CreateAdmin(ctx, "admin", "hash")
	key := make([]byte, 32)
	service, err := New(s, key)
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := providers.DefaultConfig("openai")
	if err != nil {
		t.Fatal(err)
	}
	id, err := service.Import(ctx, actor, "account", Credential{Config: cfg, Token: providers.Token{AccessToken: "fixture"}, ExpiresAt: time.Now().Add(time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	defaults, _ := service.ClientIdentity(ctx)
	defaults.CodexVersion = "0.200.0"
	if err = service.SaveClientIdentity(ctx, actor, defaults); err != nil {
		t.Fatal(err)
	}
	c, err := service.Access(ctx, id)
	if err != nil || c.Config.ClientVersion != "0.200.0" {
		t.Fatal("global version not applied", err, c.Config.ClientVersion)
	}
	info, err := service.CredentialInfo(ctx, id)
	if err != nil || info.ClientVersion != "0.200.0" {
		t.Fatal("credential info does not show effective version", info, err)
	}
	encrypted, err := service.seal(id, c)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := credentials.Decrypt(key, binding(id), encrypted)
	if err != nil {
		t.Fatal(err)
	}
	var saved Credential
	if json.Unmarshal(raw, &saved) != nil || saved.Config.ClientVersion != "" {
		t.Fatal("saving refreshed credential turned inherited version into a pin")
	}
	runtime, err := service.RuntimeConfig(ctx, id)
	if err != nil || runtime.ClientVersion != "" {
		t.Fatal("runtime form turns inherited identity into pin")
	}
	cfg.ClientVersion = "0.150.0"
	pinned := Credential{Config: cfg}
	if err = service.resolveIdentity(ctx, &pinned); err != nil || pinned.Config.ClientVersion != "0.150.0" {
		t.Fatal("explicit version overwritten")
	}
}

func TestIdentityVersionRollback(t *testing.T) {
	ctx := context.Background()
	s, err := store.Open(ctx, filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	actor, _ := s.CreateAdmin(ctx, "admin", "hash")
	service, err := New(s, make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	original, _ := service.ClientIdentity(ctx)
	next := original
	next.CodexVersion = "0.200.0"
	next.PreviousCodexVersion = "untrusted"
	if err = service.SaveClientIdentity(ctx, actor, next); err != nil {
		t.Fatal(err)
	}
	saved, _ := service.ClientIdentity(ctx)
	if saved.PreviousCodexVersion != original.CodexVersion {
		t.Fatal(saved)
	}
	if err = service.SaveClientIdentity(ctx, actor, saved); err != nil {
		t.Fatal(err)
	}
	saved, _ = service.ClientIdentity(ctx)
	if saved.PreviousCodexVersion != original.CodexVersion {
		t.Fatal("same version lost rollback", saved)
	}
	saved.CodexVersion = saved.PreviousCodexVersion
	if err = service.SaveClientIdentity(ctx, actor, saved); err != nil {
		t.Fatal(err)
	}
	saved, _ = service.ClientIdentity(ctx)
	if saved.CodexVersion != original.CodexVersion || saved.PreviousCodexVersion != "0.200.0" {
		t.Fatal(saved)
	}
}
