package store

import (
	"encoding/json"
	"errors"
	"fmt"
)

// PriceRate uses the same fixed-point unit as Price: microUSD per million tokens.
// Nil cache prices mean unpriced, never free. Rates are copied into each request.
type PriceRate struct {
	Unavailable    bool   `json:"unavailable,omitempty"`
	Tier           string `json:"tier"`
	Above          int64  `json:"above,omitempty"`
	Input          int64  `json:"input"`
	Output         int64  `json:"output"`
	Cached         *int64 `json:"cached,omitempty"`
	CacheWrite     *int64 `json:"cache_write,omitempty"`
	CacheWriteHour *int64 `json:"cache_write_hour,omitempty"`
}

type BillingUsage struct {
	Confirmed      bool   `json:"confirmed,omitempty"`
	Tier           string `json:"tier,omitempty"`
	InputTotal     int64  `json:"input_total,omitempty"`
	CacheWriteHour int64  `json:"cache_write_hour,omitempty"`
}

func NormalizeTier(tier string) string {
	switch tier {
	case "", "auto", "default", "standard", "standard_only":
		return "default"
	case "priority":
		return "fast"
	default:
		return tier
	}
}

func (p Price) rate(tier string, input int64) (PriceRate, error) {
	tier = NormalizeTier(tier)
	if len(p.Rates) == 0 {
		if tier != "default" {
			return PriceRate{}, errors.New("当前模型服务档位尚未定价")
		}
		return PriceRate{Tier: tier, Input: p.Input, Output: p.Output, Cached: &p.Cached, CacheWrite: &p.CacheWrite}, nil
	}
	var selected *PriceRate
	for i := range p.Rates {
		r := &p.Rates[i]
		if NormalizeTier(r.Tier) == tier && (r.Above == 0 || input > r.Above) && (selected == nil || r.Above > selected.Above) {
			selected = r
		}
	}
	if selected == nil || selected.Unavailable {
		return PriceRate{}, errors.New("当前模型服务档位尚未定价")
	}
	return *selected, nil
}

// ValidateTier rejects unknown tiers before dispatch. Long-context variants are
// selected again at settlement using observed input, not a body-length guess.
func (p Price) ValidateTier(tier string) error { _, err := p.rate(tier, 0); return err }

func (p Price) Maximum(tier string) (Price, error) {
	r, err := p.rate(tier, 0)
	if err != nil {
		return Price{}, err
	}
	q := Price{Input: r.Input, Output: r.Output}
	for _, v := range append([]PriceRate{r}, p.Rates...) {
		if NormalizeTier(v.Tier) != NormalizeTier(tier) {
			continue
		}
		q.Input = max(q.Input, v.Input)
		q.Output = max(q.Output, v.Output)
		for _, n := range []*int64{v.Cached, v.CacheWrite, v.CacheWriteHour} {
			if n != nil {
				q.Input = max(q.Input, *n)
			}
		}
	}
	return q, nil
}

func (p Price) costWithRules(u TokenUsage) (int64, error) {
	if u.Input < 0 || u.Output < 0 || u.Cached < 0 || u.CacheWrite < 0 || u.InputTotal < 0 || u.Cached > 9223372036854775807-u.Input || u.CacheWrite > 9223372036854775807-u.Input-u.Cached {
		return 0, errors.New("用量超出范围")
	}
	r, err := p.rate(u.Tier, max(u.InputTotal, u.Input+u.Cached+u.CacheWrite))
	if err != nil {
		return 0, err
	}
	q := Price{Input: r.Input, Output: r.Output}
	for _, v := range []struct {
		n     int64
		price *int64
		dest  *int64
	}{{u.Cached, r.Cached, &q.Cached}, {u.CacheWrite - u.CacheWriteHour, r.CacheWrite, &q.CacheWrite}} {
		if v.n < 0 {
			return 0, errors.New("缓存用量无效")
		}
		if v.n > 0 && v.price == nil {
			return 0, fmt.Errorf("当前缓存计费类型尚未定价")
		}
		if v.price != nil {
			*v.dest = *v.price
		}
	}
	if u.CacheWriteHour < 0 || u.CacheWriteHour > u.CacheWrite {
		return 0, errors.New("缓存用量无效")
	}
	if u.CacheWriteHour > 0 && r.CacheWriteHour == nil {
		return 0, errors.New("一小时缓存写入尚未定价")
	}
	// Sum all categories before rounding, including the 1h cache bucket.
	return fixedCost([][2]int64{{q.Input, u.Input}, {q.Output, u.Output}, {q.Cached, u.Cached}, {q.CacheWrite, u.CacheWrite - u.CacheWriteHour}, {valueOrZero(r.CacheWriteHour), u.CacheWriteHour}})
}
func valueOrZero(p *int64) int64 {
	if p == nil {
		return 0
	}
	return *p
}

func priceRulesJSON(p Price) string { raw, _ := json.Marshal(p); return string(raw) }

func (p Price) Validate() error {
	if p.MaxOutput < 0 || p.MaxOutput > 10000000 || len(p.Rates) > 128 {
		return errors.New("价格规则超出范围")
	}
	seen := map[string]bool{}
	for _, r := range p.Rates {
		key := fmt.Sprintf("%s:%d", NormalizeTier(r.Tier), r.Above)
		if seen[key] || r.Above < 0 || len(r.Tier) > 64 || r.Tier == "" {
			return errors.New("价格规则无效或重复")
		}
		seen[key] = true
		values := []int64{r.Input, r.Output}
		for _, n := range []*int64{r.Cached, r.CacheWrite, r.CacheWriteHour} {
			if n != nil {
				values = append(values, *n)
			}
		}
		for _, n := range values {
			if n < 0 || n > 1000000000000 {
				return errors.New("价格超出范围")
			}
		}
	}
	_, err := p.Cost(TokenUsage{})
	return err
}
