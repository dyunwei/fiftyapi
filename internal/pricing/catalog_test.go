package pricing

import (
	"context"
	"fiftyapi/internal/store"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
)

const fixture = `{
 "model":{"litellm_provider":"openai","mode":"chat","input_cost_per_token":0.000001,"output_cost_per_token":0.000002,"cache_read_input_token_cost":0.0000001,"input_cost_per_token_priority":0.000003,"output_cost_per_token_priority":0.000006,"cache_read_input_token_cost_priority":0.0000003,"input_cost_per_token_above_200k_tokens":0.000002,"output_cost_per_token_above_200k_tokens":0.000004},
 "alias":{"alias_of":"model"},
 "bad":{"litellm_provider":"openai","mode":"chat","input_cost_per_token":-1,"output_cost_per_token":2},
 "image":{"litellm_provider":"openai","mode":"image_generation","output_cost_per_image":1}
}`

func TestParsePricesAndConditions(t *testing.T) {
	prices, err := Parse([]byte(fixture))
	if err != nil {
		t.Fatal(err)
	}
	p, ok := prices["openai/model"]
	if !ok || p.Input != 1000000 || p.Output != 2000000 || p.Cached != 100000 {
		t.Fatalf("wrong fixed-point rates: %+v", p)
	}
	if _, ok = prices["alias"]; !ok {
		t.Fatal("exact alias lost")
	}
	if _, ok = prices["bad"]; ok {
		t.Fatal("invalid price imported")
	}
	if _, ok = prices["image"]; ok {
		t.Fatal("non-token price imported as token price")
	}
	for _, tc := range []struct {
		tier                        string
		input, cached, output, want int64
	}{{"default", 10, 2, 3, 17}, {"priority", 10, 2, 3, 49}, {"default", 200001, 0, 1, 400006}} {
		got, e := p.Cost(store.TokenUsage{BillingUsage: store.BillingUsage{Tier: tc.tier}, Input: tc.input, Cached: tc.cached, Output: tc.output})
		if e != nil || got != tc.want {
			t.Fatalf("tier %s: cost=%d err=%v want=%d", tc.tier, got, e, tc.want)
		}
	}
	if p.ValidateTier("ultrafast") == nil {
		t.Fatal("missing premium price treated as standard")
	}
	if _, err = p.Cost(store.TokenUsage{CacheWrite: 1}); err == nil {
		t.Fatal("missing cache write rate treated as free")
	}
}
func TestPriceSyncPersistsAndPreservesOverrides(t *testing.T) {
	ctx := context.Background()
	s, err := store.Open(ctx, filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	actor, _ := s.CreateAdmin(ctx, "admin", "hash")
	account, _ := s.CreateAccount(ctx, actor, "account", "openai", func(int64) ([]byte, error) { return []byte("ciphertext"), nil })
	if err = s.SyncModels(ctx, account, "openai", []string{"model"}); err != nil {
		t.Fatal(err)
	}
	failure := false
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if failure {
			io.WriteString(w, `{"broken":true}`)
			return
		}
		io.WriteString(w, fixture)
	}))
	defer upstream.Close()
	svc := New(s)
	svc.URL = upstream.URL
	svc.Client = upstream.Client()
	if err = svc.Sync(ctx); err != nil {
		t.Fatal(err)
	}
	p, err := s.Price(ctx, "model")
	if err != nil || p.Source != "litellm" || len(p.Rates) < 2 || p.Revision == "" {
		t.Fatalf("catalog not applied: %+v %v", p, err)
	}
	if err = svc.Sync(ctx); err != nil {
		t.Fatal(err)
	}
	var count int
	_ = s.DB.QueryRow("SELECT count(*) FROM model_prices WHERE model_id='model'").Scan(&count)
	if count != 1 {
		t.Fatal("unchanged sync duplicated price history")
	}
	if err = s.SetModel(ctx, actor, "model", true, &store.Price{Input: 7, Output: 8}); err != nil {
		t.Fatal(err)
	}
	if err = svc.Sync(ctx); err != nil {
		t.Fatal(err)
	}
	p, _ = s.Price(ctx, "model")
	if p.Source != "manual" || p.Input != 7 {
		t.Fatal("manual override overwritten")
	}
	if err = s.SetPricingKey(ctx, actor, "model", "openai/model"); err != nil {
		t.Fatal(err)
	}
	p, _ = s.Price(ctx, "model")
	if p.Source != "litellm" || p.Input != 1000000 {
		t.Fatal("return to catalog failed")
	}
	failure = true
	if err = svc.Sync(ctx); err == nil {
		t.Fatal("malformed source accepted")
	}
	after, _ := s.Price(ctx, "model")
	if after.ID != p.ID {
		t.Fatal("failure changed active price")
	}
	// A model discovered later can use already downloaded prices without network.
	if err = s.SyncModels(ctx, account, "openai", []string{"model", "alias"}); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Price(ctx, "alias"); err != nil {
		t.Fatal("newly discovered alias did not inherit catalog price", err)
	}
}
func TestMoneyPrecision(t *testing.T) {
	for _, tc := range []struct {
		raw  string
		want int64
		ok   bool
	}{{"1e-6", 1000000, true}, {"0.000000000001", 1, true}, {"0.0000000000001", 0, false}, {"-1", 0, false}, {"null", 0, false}, {"1e99", 0, false}} {
		n, ok := money([]byte(tc.raw))
		if ok != tc.ok || ok && n != tc.want {
			t.Fatalf("%s: %d %v", tc.raw, n, ok)
		}
	}
}

func TestExplicitProviderKeyWinsCanonicalCollision(t *testing.T) {
	raw := `{"model":{"litellm_provider":"openai","mode":"chat","input_cost_per_token":0.000001,"output_cost_per_token":0.000002},"openai/model":{"litellm_provider":"openai","mode":"chat","input_cost_per_token":0.000003,"output_cost_per_token":0.000004}}`
	for i := 0; i < 20; i++ {
		prices, err := Parse([]byte(raw))
		if err != nil || prices["openai/model"].Input != 3000000 {
			t.Fatal("canonical collision changed explicit prices", err)
		}
	}
}
