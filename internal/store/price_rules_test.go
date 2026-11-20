package store

import (
	"context"
	"testing"
	"time"
)

func TestTierSettlementUsesImmutableRulesAndServedTier(t *testing.T) {
	s, user, key := quotaStore(t)
	ctx := context.Background()
	cached := int64(100000)
	p := Price{Input: 1000000, Output: 2000000, Rates: []PriceRate{{Tier: "default", Input: 1000000, Output: 2000000, Cached: &cached}, {Tier: "fast", Input: 3000000, Output: 6000000, Cached: &cached}}}
	if err := s.Reserve(ctx, "tier", user, key, "model", p, 50, time.Now()); err != nil {
		t.Fatal(err)
	}
	l := accountingLog("tier", user, key, 10, 3)
	l.BillingUsage = BillingUsage{Tier: "default", Confirmed: true, InputTotal: 10}
	// Mutating the live rules must not mutate the request snapshot.
	p.Rates[0].Input = 99000000
	if err := s.FinalizeRequest(ctx, l); err != nil {
		t.Fatal(err)
	}
	if err := s.FinalizeRequest(ctx, l); err != nil {
		t.Fatal(err)
	}
	windows, _ := s.Quota(ctx, user)
	if windows[0].Used != 16 || windows[0].Reserved != 0 {
		t.Fatal("wrong served-tier settlement", windows)
	}
	logs, _ := s.Logs(ctx, user, false, 0, "", 0)
	if len(logs) != 1 || !logs[0].BillingUsage.Confirmed {
		t.Fatal("served tier evidence missing")
	}
}
func TestUnpricedActualTierSettlesFreeAndReleasesReservation(t *testing.T) {
	s, user, key := quotaStore(t)
	ctx := context.Background()
	p := Price{Input: 1000000, Output: 1000000}
	if err := s.Reserve(ctx, "unknown-tier", user, key, "model", p, 50, time.Now()); err != nil {
		t.Fatal(err)
	}
	l := accountingLog("unknown-tier", user, key, 10, 3)
	l.BillingUsage = BillingUsage{Tier: "ultrafast", Confirmed: true}
	if err := s.FinalizeRequest(ctx, l); err != nil {
		t.Fatal(err)
	}
	pending, _ := s.PendingCharges(ctx)
	windows, _ := s.Quota(ctx, user)
	if len(pending) != 0 || windows[0].Used != 0 || windows[0].Reserved != 0 {
		t.Fatal("unpriced request remained pending or affected quota", pending, windows)
	}
}
func TestCacheWriteDurationAndSingleRounding(t *testing.T) {
	short, hour := int64(1250000), int64(2000000)
	p := Price{Rates: []PriceRate{{Tier: "default", Input: 1000000, Output: 2000000, CacheWrite: &short, CacheWriteHour: &hour}}}
	got, err := p.Cost(TokenUsage{Input: 1, Output: 1, CacheWrite: 3, BillingUsage: BillingUsage{CacheWriteHour: 2}})
	if err != nil || got != 9 {
		t.Fatalf("cache duration cost=%d err=%v", got, err)
	}
	if _, err = p.Cost(TokenUsage{CacheWrite: 1, BillingUsage: BillingUsage{CacheWriteHour: 2}}); err == nil {
		t.Fatal("invalid cache breakdown accepted")
	}
}
