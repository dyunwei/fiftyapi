package accounts

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fiftyapi/internal/providers"
	"fiftyapi/internal/store"
	"net"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

type Sub2APIAccount struct {
	ID          int64           `json:"id"`
	Name        string          `json:"name"`
	Platform    string          `json:"platform"`
	Type        string          `json:"type"`
	Status      string          `json:"status"`
	Credentials json.RawMessage `json:"credentials"`
	ProxyID     *int64          `json:"proxy_id"`
	Concurrency int             `json:"concurrency"`
	Priority    int             `json:"priority"`
	ExpiresAt   *time.Time      `json:"expires_at"`
}
type Sub2APIProxy struct {
	ID        int64      `json:"id"`
	Protocol  string     `json:"protocol"`
	Host      string     `json:"host"`
	Port      int        `json:"port"`
	Username  string     `json:"username"`
	Password  string     `json:"password"`
	Status    string     `json:"status"`
	ExpiresAt *time.Time `json:"expires_at"`
}

func sub2apiCredential(a Sub2APIAccount, proxies map[int64]Sub2APIProxy) (Credential, []byte, store.AccountPolicy, error) {
	fail := func() (Credential, []byte, store.AccountPolicy, error) {
		return Credential{}, nil, store.AccountPolicy{}, errors.New("账号类型或连接配置暂不支持，请手动添加")
	}
	if a.ID <= 0 || strings.TrimSpace(a.Name) == "" || len(a.Name) > 128 || strings.ContainsAny(a.Name, "\r\n\x00") {
		return fail()
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(a.Credentials, &fields) != nil || fields == nil {
		return fail()
	}
	text := func(key string) string { var v string; _ = json.Unmarshal(fields[key], &v); return v }
	var c Credential
	var fingerprint []byte
	provider := a.Platform
	if provider == "anthropic" {
		provider = "claude"
	}
	switch a.Type {
	case "oauth":
		var err error
		c, fingerprint, err = ParseAuth(provider, a.Credentials)
		if err != nil {
			return fail()
		}
		if provider == "antigravity" && text("project_id") != "" {
			var body map[string]json.RawMessage
			if json.Unmarshal(c.Config.ModelsBody, &body) != nil || body == nil {
				body = map[string]json.RawMessage{}
			}
			body["project"], _ = json.Marshal(text("project_id"))
			c.Config.ModelsBody, _ = json.Marshal(body)
		}
	case "apikey", "api_key":
		protocol, auth, base := "", "bearer", text("base_url")
		switch provider {
		case "openai":
			protocol = "responses"
			if base == "" {
				base = "https://api.openai.com"
			}
		case "claude":
			protocol = "messages"
			auth = "x-api-key"
			if base == "" {
				base = "https://api.anthropic.com"
			}
		default:
			return fail()
		}
		normalized, err := providers.NormalizeGatewayBase(base, false)
		if err != nil {
			return fail()
		}
		g := &providers.GatewayConfig{Platform: "custom", BaseURL: normalized, Protocols: []string{protocol}, AuthMode: auth, Discovery: "auto"}
		if provider == "openai" {
			g.Protocols = []string{"chat", "responses"}
		}
		var mapping map[string]string
		if raw, ok := fields["model_mapping"]; ok && string(raw) != "null" {
			if json.Unmarshal(raw, &mapping) != nil {
				return fail()
			}
		}
		names := make([]string, 0, len(mapping))
		for name := range mapping {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			target := mapping[name]
			if !providers.ValidModelID(name) || !providers.ValidModelID(target) || strings.ContainsAny(name+target, "*?") {
				return fail()
			}
			g.Models = append(g.Models, providers.GatewayModel{ID: name, UpstreamID: target, Protocols: g.Protocols})
		}
		if len(g.Models) > 0 {
			g.Discovery = "manual"
		}
		c = Credential{Config: providers.Config{Provider: "gateway", Gateway: g}, APIKey: text("api_key")}
		if c.APIKey == "" || len(c.APIKey) > 4096 || strings.ContainsAny(c.APIKey, "\r\n") || strings.TrimSpace(c.APIKey) != c.APIKey {
			return fail()
		}
		hash := sha256.Sum256([]byte("sub2api-api-key\x00" + normalized + "\x00" + c.APIKey))
		fingerprint = hash[:]
	default:
		return fail()
	}
	if a.ProxyID != nil {
		p, ok := proxies[*a.ProxyID]
		if !ok || p.Status != "active" || p.Port < 1 || p.Port > 65535 || p.Host == "" || strings.ContainsAny(p.Host, "/@?#\r\n") || (p.ExpiresAt != nil && !p.ExpiresAt.After(time.Now())) {
			return fail()
		}
		u := url.URL{Scheme: p.Protocol, Host: net.JoinHostPort(p.Host, strconv.Itoa(p.Port))}
		if p.Username != "" || p.Password != "" {
			u.User = url.UserPassword(p.Username, p.Password)
		}
		c.Config.ProxyURL = u.String()
	}
	if err := c.Config.Validate(); err != nil {
		return fail()
	}
	policy := store.DefaultAccountPolicy()
	if a.Concurrency > 0 {
		policy.Concurrency = min(a.Concurrency, 100)
	}
	// Both schedulers prefer smaller priorities; retain the supported range.
	policy.Priority = max(0, min(a.Priority, 100))
	return c, fingerprint, policy, nil
}

// ImportSub2API never verifies or refreshes tokens during preview or saving.
// The administrator verifies after stopping old-site refresh jobs.
func (s *Service) ImportSub2API(ctx context.Context, actor int64, accounts []Sub2APIAccount, proxies []Sub2APIProxy, preview bool) ([]store.MigrationItem, error) {
	proxyMap := map[int64]Sub2APIProxy{}
	for _, p := range proxies {
		if _, ok := proxyMap[p.ID]; ok {
			return nil, errors.New("duplicate proxy ID")
		}
		proxyMap[p.ID] = p
	}
	out := make([]store.MigrationItem, 0, len(accounts))
	seen := map[string]bool{}
	seenID := map[int64]bool{}
	for _, a := range accounts {
		item := store.MigrationItem{Kind: "account", SourceID: a.ID, Name: a.Name, Status: "skipped"}
		c, fp, policy, err := sub2apiCredential(a, proxyMap)
		if err != nil {
			item.Warning = "账号类型或连接配置暂不支持，请手动添加"
			out = append(out, item)
			continue
		}
		var exists bool
		if err = s.store.DB.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM upstream_accounts WHERE deleted_at IS NULL AND (name=? OR auth_fingerprint=?))", a.Name, fp).Scan(&exists); err != nil {
			return out, err
		}
		if exists || seen[a.Name] || seen[string(fp)] || seenID[a.ID] {
			item.Warning = "账号名称或授权已存在，保留现有配置"
			out = append(out, item)
			continue
		}
		seen[a.Name] = true
		seen[string(fp)] = true
		seenID[a.ID] = true
		disabled := a.Status != "active" || (a.ExpiresAt != nil && !a.ExpiresAt.After(time.Now()))
		if !preview {
			item.ID, err = s.importWithOrigin(ctx, actor, a.Name, c, store.AccountOrigin{Fingerprint: fp, Disabled: disabled, Policy: &policy})
			if err != nil {
				item.Warning = "保存失败，请重试；现有账号不会被覆盖"
				out = append(out, item)
				continue
			}
		}
		item.Status = "imported"
		item.Enabled = !disabled
		item.Warning = "等待验证并同步模型；旧站分组和高级策略未迁移"
		out = append(out, item)
	}
	return out, nil
}
