package accounts

import (
	"context"
	"encoding/json"
	"errors"
	"fiftyapi/internal/providers"
	"fiftyapi/internal/store"
	"io"
	"math"
	"net/http"
	"net/url"
	"time"
)

func (s *Service) QueryOpenAIQuota(ctx context.Context, id int64) (health store.AccountHealth, outcome error) {
	defer s.runtimeOutcome(ctx, id, "quota_query", "上游额度查询", time.Now(), &outcome)
	c, err := s.Access(ctx, id)
	if err != nil {
		return store.AccountHealth{}, err
	}
	if c.Config.Provider != "openai" {
		return store.AccountHealth{}, errors.New("此账号未提供可用的主动额度查询协议")
	}
	u, err := url.Parse(c.Config.ModelsURL)
	if err != nil || u.Scheme != "https" || u.User != nil {
		return store.AccountHealth{}, errors.New("上游额度地址无效")
	}
	u.Path = "/backend-api/wham/usage"
	u.RawPath = ""
	u.RawQuery = ""
	u.Fragment = ""
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return store.AccountHealth{}, errors.New("无法创建额度查询")
	}
	request.Header.Set("Authorization", "Bearer "+c.Token.AccessToken)
	request.Header.Set("Accept", "application/json")
	providers.IdentityHeaders(request.Header, c.Config)
	providers.SetOpenAIAccountHeader(request.Header, c.Config, c.Token)
	client := s.client
	if c.Config.ProxyURL != "" || c.Config.TimeoutSeconds != 0 {
		client = providers.HTTPClient(c.Config)
		defer client.CloseIdleConnections()
	}
	resp, err := client.Do(request)
	if err != nil {
		return store.AccountHealth{}, errors.New("上游额度查询连接失败")
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return store.AccountHealth{}, errors.New("上游未接受额度查询；未输出响应原文")
	}
	var body struct {
		RateLimit *struct {
			Primary   *quotaWindow `json:"primary_window"`
			Secondary *quotaWindow `json:"secondary_window"`
		} `json:"rate_limit"`
	}
	if json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&body) != nil || body.RateLimit == nil {
		return store.AccountHealth{}, errors.New("上游额度响应无效")
	}
	var primary, secondary *float64
	var primaryReset, secondaryReset *int64
	decode := func(w *quotaWindow) (*float64, *int64, error) {
		if w == nil {
			return nil, nil, nil
		}
		if w.Used == nil || *w.Used < 0 || *w.Used > 100 || math.IsNaN(*w.Used) || math.IsInf(*w.Used, 0) {
			return nil, nil, errors.New("上游额度数据超出范围")
		}
		now := time.Now().Unix()
		var reset *int64
		if w.ResetAt > 0 && w.ResetAt >= now && w.ResetAt <= now+30*86400 {
			reset = &w.ResetAt
		} else if w.After != nil && *w.After >= 0 && *w.After <= 30*86400 {
			n := now + *w.After
			reset = &n
		}
		return w.Used, reset, nil
	}
	if primary, primaryReset, err = decode(body.RateLimit.Primary); err != nil {
		return store.AccountHealth{}, err
	}
	if secondary, secondaryReset, err = decode(body.RateLimit.Secondary); err != nil {
		return store.AccountHealth{}, err
	}
	if primary == nil && secondary == nil {
		return store.AccountHealth{}, errors.New("上游没有返回可计量窗口，保留已有快照")
	}
	if err = s.store.SaveQuotaSnapshot(ctx, id, primary, primaryReset, secondary, secondaryReset); err != nil {
		return store.AccountHealth{}, err
	}
	return s.store.AccountHealth(ctx, id)
}

type quotaWindow struct {
	Used    *float64 `json:"used_percent"`
	ResetAt int64    `json:"reset_at"`
	After   *int64   `json:"reset_after_seconds"`
}
