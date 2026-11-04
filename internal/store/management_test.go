package store

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"
)

func TestDeletedKeyCannotAuthenticateAndPreservesCharges(t *testing.T) {
	s, user, _ := quotaStore(t)
	ctx := context.Background()
	k, token, err := s.CreateAPIKey(ctx, user, "delete-me")
	if err != nil {
		t.Fatal(err)
	}
	if err = s.DeleteAPIKey(ctx, user+100, k.ID); !errors.Is(err, ErrNotFound) {
		t.Fatal("another user deleted key", err)
	}
	if _, _, err = s.AuthenticateAPIKey(ctx, token); err != nil {
		t.Fatal(err)
	}
	if err = s.Reserve(ctx, "in-flight", user, k.ID, "model", Price{}, 20, time.Now()); err != nil {
		t.Fatal(err)
	}
	if err = s.DeleteAPIKey(ctx, user, k.ID); err != nil {
		t.Fatal(err)
	}
	if _, _, err = s.AuthenticateAPIKey(ctx, token); err == nil {
		t.Fatal("deleted key authenticated")
	}
	if err = s.Reserve(ctx, "after-delete", user, k.ID, "model", Price{}, 1, time.Now()); err == nil {
		t.Fatal("deleted key reserved quota")
	}
	if err = s.RevokeAPIKey(ctx, user, k.ID); !errors.Is(err, ErrNotFound) {
		t.Fatal("deleted key still manageable")
	}
	if err = s.Settle(ctx, "in-flight", 10, time.Now()); err != nil {
		t.Fatal("deletion broke settlement", err)
	}
	if err = s.LogRequest(ctx, RequestLog{ID: "in-flight", UserID: user, KeyID: k.ID, Model: "model", Provider: "antigravity", Status: 200, CreatedAt: time.Now().Unix()}); err != nil {
		t.Fatal(err)
	}
	keys, err := s.APIKeys(ctx, user)
	if err != nil {
		t.Fatal(err)
	}
	for _, listed := range keys {
		if listed.ID == k.ID {
			t.Fatal("deleted key listed")
		}
	}
	var name, prefix string
	var enabled int
	if err = s.DB.QueryRow("SELECT name,prefix,enabled FROM api_keys WHERE id=?", k.ID).Scan(&name, &prefix, &enabled); err != nil {
		t.Fatal(err)
	}
	if name != "[deleted]" || prefix != "" || enabled != 0 {
		t.Fatal("key identifying data retained")
	}
	d, err := s.Dashboard(ctx, user, false, 24, 3600, 0)
	if err != nil || d.Keys != 1 {
		t.Fatal("deleted key counted", err, d.Keys)
	}
	if err = s.DeleteAPIKey(ctx, user, k.ID); !errors.Is(err, ErrNotFound) {
		t.Fatal("repeat deletion accepted")
	}
	logs, err := s.Logs(ctx, user, false, 0, "", 0)
	if err != nil || len(logs) != 1 || logs[0].KeyID != k.ID {
		t.Fatal("history lost", err)
	}
}

func TestModelSearchPaginationAndLatestPrice(t *testing.T) {
	s, user, _ := quotaStore(t)
	ctx := context.Background()
	account, err := s.CreateAccount(ctx, user, "catalog", "antigravity", func(int64) ([]byte, error) { return []byte("test"), nil }, "")
	if err != nil {
		t.Fatal(err)
	}
	ids := []string{}
	for i := 0; i < 25; i++ {
		ids = append(ids, fmt.Sprintf("gemini-%02d", i))
	}
	ids = append(ids, "literal_%")
	if err = s.SyncModels(ctx, account, "antigravity", ids); err != nil {
		t.Fatal(err)
	}
	if err = s.AccountState(ctx, account, "catalog_ready", ""); err != nil {
		t.Fatal(err)
	}
	for _, id := range ids {
		if err = s.SetModel(ctx, user, id, true, &Price{Input: 1, Output: 2}); err != nil {
			t.Fatal(err)
		}
	}
	if err = s.SetModel(ctx, user, ids[0], true, &Price{Input: 9, Output: 10}); err != nil {
		t.Fatal(err)
	}
	other, err := s.CreateAccount(ctx, user, "other", "openai", func(int64) ([]byte, error) { return []byte("test"), nil }, "")
	if err != nil {
		t.Fatal(err)
	}
	if err = s.SyncModels(ctx, other, "openai", []string{"different-model"}); err != nil {
		t.Fatal(err)
	}
	filtered, err := s.ModelPage(ctx, 2, 10, "gemini", false, ModelFilter{AccountID: account, Protocol: "messages"})
	if err != nil || filtered.Total != 25 || len(filtered.Items) != 10 {
		t.Fatal("combined account/protocol pagination", err, filtered)
	}
	for _, f := range []ModelFilter{{AccountID: other}, {Protocol: "responses"}, {AccountID: account, Protocol: "chat"}} {
		result, err := s.ModelPage(ctx, 1, 10, "gemini", false, f)
		if err != nil || result.Total != 0 {
			t.Fatal("filter leaked unmatched models", f, err, result)
		}
	}
	first, err := s.ModelPage(ctx, 1, 10, " GEMINI ", false)
	if err != nil || first.Total != 25 || len(first.Items) != 10 {
		t.Fatal("search/page failed", err, first)
	}
	if first.Items[0].Price.Input != 9 {
		t.Fatal("stale price selected")
	}
	second, err := s.ModelPage(ctx, 2, 10, "gemini", false)
	if err != nil || second.Items[0].ID == first.Items[0].ID {
		t.Fatal("pagination overlap", err)
	}
	last, err := s.ModelPage(ctx, 3, 10, "gemini", false)
	if err != nil || len(last.Items) != 5 {
		t.Fatal("last page", err)
	}
	literal, err := s.ModelPage(ctx, 1, 20, "_%", false)
	if err != nil || literal.Total != 1 {
		t.Fatal("search treated literal wildcard as pattern", err)
	}
	if err = s.SetModel(ctx, user, ids[1], false, nil); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		status string
		total  int
	}{{"priced", 25}, {"disabled", 1}, {"unavailable", 1}, {"unpriced", 0}} {
		result, err := s.ModelPage(ctx, 1, 10, "", false, ModelFilter{Status: tc.status})
		if err != nil || result.Total != tc.total {
			t.Fatal("status filter", tc.status, err, result.Total)
		}
	}
	public, err := s.ModelPage(ctx, 1, 100, "antigravity", true)
	if err != nil || public.Total != 25 {
		t.Fatal("public filter failed", err, public.Total)
	}
	if _, err = s.ModelPage(ctx, 0, 10, "", false); err == nil {
		t.Fatal("invalid page accepted")
	}
}
