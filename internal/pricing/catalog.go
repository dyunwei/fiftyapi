// Package pricing imports data only; no LiteLLM code executes in the gateway.
package pricing

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fiftyapi/internal/store"
	"io"
	"math/big"
	"net/http"
	"sort"
	"strings"
	"time"
)

const CatalogURL = "https://raw.githubusercontent.com/BerriAI/litellm/main/model_prices_and_context_window.json"
const MaxCatalogBytes = 32 << 20

// Parse accepts USD/token decimals without passing money through float64.
func Parse(data []byte) (map[string]store.Price, error) {
	var entries map[string]map[string]json.RawMessage
	if json.Unmarshal(data, &entries) != nil || len(entries) == 0 {
		return nil, errors.New("价格目录格式无效")
	}
	out := map[string]store.Price{}
	for key, e := range entries {
		var provider, mode string
		_ = json.Unmarshal(e["litellm_provider"], &provider)
		_ = json.Unmarshal(e["mode"], &mode)
		if provider == "" || (mode != "chat" && mode != "completion") {
			continue
		}
		rates := []store.PriceRate{}
		for _, tier := range []struct{ name, suffix string }{{"default", ""}, {"fast", "_priority"}, {"fast", "_fast"}, {"ultrafast", "_ultrafast"}, {"flex", "_flex"}} {
			for _, threshold := range []struct {
				n      int64
				suffix string
			}{{0, ""}, {128000, "_above_128k_tokens"}, {200000, "_above_200k_tokens"}, {272000, "_above_272k_tokens"}} {
				lookup := func(prefix string) json.RawMessage {
					raw := e[prefix+tier.suffix+threshold.suffix]
					if len(raw) == 0 {
						raw = e[prefix+threshold.suffix+tier.suffix]
					}
					return raw
				}
				inputRaw, outputRaw := lookup("input_cost_per_token"), lookup("output_cost_per_token")
				if len(inputRaw) == 0 && len(outputRaw) == 0 {
					continue
				}
				input, inputOK := money(inputRaw)
				output, outputOK := money(outputRaw)
				r := store.PriceRate{Tier: tier.name, Above: threshold.n, Input: input, Output: output, Unavailable: !inputOK || !outputOK}
				r.Cached = optionalMoney(lookup("cache_read_input_token_cost"))
				r.CacheWrite = optionalMoney(lookup("cache_creation_input_token_cost"))
				r.CacheWriteHour = optionalMoney(lookup("cache_creation_input_token_cost_above_1hr"))

				// Do not guess that standard cache rates also apply to premium tiers.
				replaced := false
				for i := range rates {
					if rates[i].Tier == r.Tier && rates[i].Above == r.Above {
						rates[i] = r
						replaced = true
						break
					}
				}
				if !replaced {
					rates = append(rates, r)
				}
			}
		}
		if len(rates) == 0 || rates[0].Tier != "default" || rates[0].Above != 0 || rates[0].Unavailable {
			continue
		}
		var maxOutput int64
		_ = json.Unmarshal(e["max_output_tokens"], &maxOutput)
		if maxOutput < 0 || maxOutput > 10000000 {
			maxOutput = 0
		}
		base := rates[0]
		p := store.Price{MaxOutput: maxOutput, Source: "litellm", Input: base.Input, Output: base.Output, Rates: rates}
		if base.Cached != nil {
			p.Cached = *base.Cached
		}
		if base.CacheWrite != nil {
			p.CacheWrite = *base.CacheWrite
		}
		if err := p.Validate(); err != nil {
			continue
		}
		// Keep the source key exact; also expose a provider-qualified canonical key.
		out[key] = p
		normalized := provider + "/" + strings.TrimPrefix(key, provider+"/")
		if _, explicit := entries[normalized]; !explicit || normalized == key {
			out[normalized] = p
		}
	}
	// Alias entries carry no prices. Resolve only exact, bounded references.
	for pass := 0; pass < 8; pass++ {
		changed := false
		for key, e := range entries {
			if _, ok := out[key]; ok {
				continue
			}
			var alias string
			_ = json.Unmarshal(e["alias_of"], &alias)
			if p, ok := out[alias]; ok {
				out[key] = p
				var provider string
				_ = json.Unmarshal(entries[alias]["litellm_provider"], &provider)
				if provider != "" {
					out[provider+"/"+strings.TrimPrefix(key, provider+"/")] = p
				}
				changed = true
			}
		}
		if !changed {
			break
		}
	}
	if len(out) == 0 {
		return nil, errors.New("价格目录没有可用的文本模型价格")
	}
	return out, nil
}
func optionalMoney(raw json.RawMessage) *int64 {
	n, ok := money(raw)
	if !ok {
		return nil
	}
	return &n
}
func money(raw json.RawMessage) (int64, bool) {
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		return 0, false
	}
	r, ok := new(big.Rat).SetString(string(raw))
	if !ok || r.Sign() < 0 {
		return 0, false
	}
	r.Mul(r, big.NewRat(1000000000000, 1))
	// Reject prices that cannot be represented exactly in the ledger unit.
	if !r.IsInt() || !r.Num().IsInt64() {
		return 0, false
	}
	n := r.Num().Int64()
	return n, n <= 1000000000000
}

type Service struct {
	Store  *store.Store
	Client *http.Client
	URL    string
}

func New(s *store.Store) *Service {
	return &Service{Store: s, Client: &http.Client{Timeout: 30 * time.Second}, URL: CatalogURL}
}
func (s *Service) Sync(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.URL, nil)
	if err != nil {
		return err
	}
	resp, err := s.Client.Do(req)
	if err != nil {
		return errors.New("无法下载价格目录")
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return errors.New("价格目录下载失败")
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, MaxCatalogBytes+1))
	if err != nil || len(data) > MaxCatalogBytes {
		return errors.New("价格目录过大或读取失败")
	}
	prices, err := Parse(data)
	if err != nil {
		return err
	}
	hash := sha256.Sum256(data)
	revision := hex.EncodeToString(hash[:])
	return s.Store.SavePriceCatalog(ctx, prices, revision)
}
func (s *Service) Maintain(ctx context.Context) {
	state, err := s.Store.MaintenanceState(ctx, "prices")
	if err != nil {
		return
	}
	now := time.Now().Unix()
	if now-state.LastSuccess < 86400 || now-state.LastAttempt < 3600 {
		return
	}
	task, cancel := context.WithTimeout(ctx, 40*time.Second)
	defer cancel()
	err = s.Sync(task)
	note := ""
	if err != nil {
		note = err.Error()
	}
	_ = s.Store.RecordMaintenance(ctx, "prices", note)
	if err != nil {
		s.Store.RecordRuntime(ctx, store.RuntimeLog{Level: "warn", Module: "pricing", Event: "sync_failed", Message: "价格同步失败，继续使用本地价格"})
	}
}

// Keys is used by tests and administrative import tooling.
func Keys(prices map[string]store.Price) []string {
	keys := make([]string, 0, len(prices))
	for k := range prices {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
