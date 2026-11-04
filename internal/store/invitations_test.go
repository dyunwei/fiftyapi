package store

import (
	"context"
	"crypto/sha256"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestInvitationLifecycleAndAtomicConsumption(t *testing.T) {
	ctx := context.Background()
	s, err := Open(ctx, filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	admin, err := s.CreateAdmin(ctx, "admin", "hash")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	first := sha256.Sum256([]byte("first-invitation"))
	second := sha256.Sum256([]byte("second-invitation"))
	user, err := s.CreateInvitedUser(ctx, admin, "member@example.test", "Member", 100, 1000, first[:], now)
	if err != nil {
		t.Fatal(err)
	}
	u, err := s.UserByName(ctx, "member@example.test")
	if err != nil || u.PasswordSet || u.Email == nil {
		t.Fatal("invited user state invalid")
	}
	if _, err = s.Invitation(ctx, first[:], now.Add(24*time.Hour)); !errors.Is(err, ErrInvitation) {
		t.Fatal("expired invitation accepted")
	}
	if _, err = s.RenewInvitation(ctx, admin, user, second[:], now); !errors.Is(err, ErrInviteCooldown) {
		t.Fatal("resend cooldown missing")
	}
	if err = s.InvitationDelivery(ctx, user, first[:], false); err != nil {
		t.Fatal(err)
	}
	if _, err = s.RenewInvitation(ctx, admin, user, second[:], now); err != nil {
		t.Fatal("failed mail could not be retried")
	}
	if _, err = s.Invitation(ctx, first[:], now); !errors.Is(err, ErrInvitation) {
		t.Fatal("old invitation survived resend")
	}
	if err = s.InvitationDelivery(ctx, user, first[:], true); err != nil {
		t.Fatal(err)
	}
	users, err := s.Users(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if users[1].MailState != "sending" {
		t.Fatal("stale send updated newer invitation state")
	}
	if err = s.EnableUser(ctx, admin, user, false); err != nil {
		t.Fatal(err)
	}
	if err = s.AcceptInvitation(ctx, second[:], "new-hash", now); !errors.Is(err, ErrInvitation) {
		t.Fatal("disabled user redeemed invitation")
	}
	if err = s.EnableUser(ctx, admin, user, true); err != nil {
		t.Fatal(err)
	}
	third := sha256.Sum256([]byte("third-invitation"))
	if _, err = s.RenewInvitation(ctx, admin, user, third[:], now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	out := make(chan error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); out <- s.AcceptInvitation(ctx, third[:], "new-hash", now.Add(time.Minute)) }()
	}
	wg.Wait()
	close(out)
	success := 0
	for e := range out {
		if e == nil {
			success++
		} else if !errors.Is(e, ErrInvitation) {
			t.Fatal(e)
		}
	}
	if success != 1 {
		t.Fatalf("invitation accepted %d times", success)
	}
	u, err = s.UserByName(ctx, "member@example.test")
	if err != nil || !u.PasswordSet || u.PasswordHash != "new-hash" {
		t.Fatal("password activation lost")
	}
	users, err = s.Users(ctx)
	if err != nil || users[1].InvitationState != "accepted" {
		t.Fatal("accepted status not displayed")
	}
}
