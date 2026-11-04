package store

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
)

func TestRegistrationAtomicityAndExistingIdentity(t *testing.T) {
	ctx := context.Background()
	s, err := Open(ctx, filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if _, err = s.RegisterUser(ctx, "new@example.com", "张三", "hash"); !errors.Is(err, ErrRegistrationUnavailable) {
		t.Fatalf("setup bypass: %v", err)
	}
	admin, err := s.CreateAdmin(ctx, "admin", "hash")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.CreateUser(ctx, admin, "OLD@EXAMPLE.COM", "old-hash", 10, 20); err != nil {
		t.Fatal(err)
	}
	if _, err = s.RegisterUser(ctx, "old@example.com", "李四", "new-hash"); !errors.Is(err, ErrUserExists) {
		t.Fatalf("existing identity conflict: %v", err)
	}
	if _, err = s.DB.ExecContext(ctx, `CREATE TRIGGER reject_registration_audit BEFORE INSERT ON admin_audit_logs WHEN NEW.action='register_user' BEGIN SELECT RAISE(ABORT,'fixture'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err = s.RegisterUser(ctx, "new@example.com", "张三", "hash"); err == nil {
		t.Fatal("registration survived failed audit")
	}
	if _, err = s.UserByName(ctx, "new@example.com"); !errors.Is(err, ErrNotFound) {
		t.Fatal("partial registration was committed")
	}
	old, err := s.UserByName(ctx, "OLD@EXAMPLE.COM")
	if err != nil || old.Limit5h != 10 || old.Limit7d != 20 || old.PasswordHash != "old-hash" {
		t.Fatal("existing account modified")
	}
}
