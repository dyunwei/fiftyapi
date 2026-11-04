package accounts

import (
	"context"
	"encoding/json"
	"errors"
	"fiftyapi/internal/credentials"
	"fiftyapi/internal/providers"
	"fiftyapi/internal/store"
	"net/url"
	"strings"
	"time"
)

type RuntimeConfig struct {
	InferencePolicy             providers.InferencePolicy `json:"inference_policy"`
	OpenAIAccountID             string                    `json:"openai_account_id"`
	Gateway                     *providers.GatewayConfig  `json:"gateway,omitempty"`
	APIKey                      string                    `json:"api_key,omitempty"`
	MetadataPassthrough         *bool                     `json:"metadata_passthrough"`
	FingerprintUnification      *bool                     `json:"fingerprint_unification"`
	ClaudeSystemPromptInjection *bool                     `json:"claude_system_prompt_injection"`
	ClaudeSystemPrompt          string                    `json:"claude_oauth_system_prompt"`
	GatewayURL                  string                    `json:"gateway_url"`
	ProxyURL                    string                    `json:"proxy_url"`
	GatewayHeaders              map[string]string         `json:"gateway_headers"`
	ModelsHeaders               map[string]string         `json:"models_headers"`
	UserAgent                   string                    `json:"user_agent"`
	ClientVersion               string                    `json:"client_version"`
	// Read-only compatibility field. PUT ignores it; scheduling owns concurrency.
	MaxConcurrency       int      `json:"max_concurrency"`
	TimeoutSeconds       int      `json:"timeout_seconds"`
	AntigravityVersion   string   `json:"antigravity_user_agent_version"`
	AntigravityIdentity  string   `json:"antigravity_identity_prompt"`
	FallbackURLs         []string `json:"antigravity_fallback_urls"`
	IdentityPatchEnabled *bool    `json:"identity_patch_enabled"`
}

func maskProxy(raw string) string {
	u, e := url.Parse(raw)
	if e != nil {
		return ""
	}
	if u.User != nil {
		if _, ok := u.User.Password(); ok {
			u.User = url.UserPassword(u.User.Username(), "***")
		}
	}
	return u.String()
}
func (s *Service) RuntimeConfig(ctx context.Context, id int64) (RuntimeConfig, error) {
	row, e := s.store.Account(ctx, id)
	if e != nil {
		return RuntimeConfig{}, e
	}
	b, e := credentials.Decrypt(s.key, binding(id), row.Credentials)
	if e != nil {
		return RuntimeConfig{}, e
	}
	var saved Credential
	if json.Unmarshal(b, &saved) != nil {
		return RuntimeConfig{}, errors.New("账号配置无效")
	}
	c := providers.NormalizeOpenAIConfig(saved.Config)
	if e = s.ensurePolicy(ctx, id, c.MaxConcurrency); e != nil {
		return RuntimeConfig{}, e
	}
	policy, e := s.store.AccountPolicy(ctx, id)
	if e != nil {
		return RuntimeConfig{}, e
	}
	c.MaxConcurrency = policy.Concurrency
	return RuntimeConfig{InferencePolicy: c.InferencePolicy, OpenAIAccountID: c.OpenAIAccountID, Gateway: c.Gateway, MetadataPassthrough: c.MetadataPassthrough, FingerprintUnification: c.FingerprintUnification, ClaudeSystemPromptInjection: c.ClaudeSystemPromptInjection, ClaudeSystemPrompt: c.ClaudeSystemPrompt, GatewayURL: c.GatewayURL, ProxyURL: maskProxy(c.ProxyURL), GatewayHeaders: c.GatewayHeaders, ModelsHeaders: c.ModelsHeaders, UserAgent: c.UserAgent, ClientVersion: c.ClientVersion, MaxConcurrency: c.MaxConcurrency, TimeoutSeconds: c.TimeoutSeconds, AntigravityVersion: c.AntigravityVersion, AntigravityIdentity: c.AntigravityIdentity, FallbackURLs: c.AntigravityFallbackURLs, IdentityPatchEnabled: c.IdentityPatchEnabled}, nil
}
func (s *Service) UpdateRuntime(ctx context.Context, actor, id int64, in RuntimeConfig) error {
	lock := make(chan struct{}, 1)
	lock <- struct{}{}
	v, _ := s.locks.LoadOrStore(id, lock)
	lock = v.(chan struct{})
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-lock:
	}
	defer func() { lock <- struct{}{} }()
	row, e := s.store.Account(ctx, id)
	if e != nil {
		return e
	}
	plain, e := credentials.Decrypt(s.key, binding(id), row.Credentials)
	if e != nil {
		return e
	}
	var saved Credential
	if json.Unmarshal(plain, &saved) != nil {
		return errors.New("账号配置无效")
	}
	c := &saved.Config
	// Initialize legacy settings once, before editing any connection fields.
	if e = s.ensurePolicy(ctx, id, c.MaxConcurrency); e != nil {
		return e
	}
	if c.Provider == "gateway" {
		c.Gateway = in.Gateway
		if in.APIKey != "" {
			if len(in.APIKey) > 4096 || strings.TrimSpace(in.APIKey) != in.APIKey || strings.ContainsAny(in.APIKey, "\r\n") {
				return errors.New("API Key 无效")
			}
			saved.APIKey = in.APIKey
		}
	}
	if in.ProxyURL != maskProxy(c.ProxyURL) {
		c.ProxyURL = in.ProxyURL
	}
	c.InferencePolicy = in.InferencePolicy
	c.GatewayURL = in.GatewayURL
	c.OpenAIAccountID = in.OpenAIAccountID
	c.GatewayHeaders = in.GatewayHeaders
	c.ModelsHeaders = in.ModelsHeaders
	c.UserAgent = in.UserAgent
	c.ClientVersion = in.ClientVersion
	c.TimeoutSeconds = in.TimeoutSeconds
	c.AntigravityVersion = in.AntigravityVersion
	c.AntigravityIdentity = in.AntigravityIdentity
	c.AntigravityFallbackURLs = in.FallbackURLs
	c.IdentityPatchEnabled = in.IdentityPatchEnabled
	c.MetadataPassthrough = in.MetadataPassthrough
	c.FingerprintUnification = in.FingerprintUnification
	c.ClaudeSystemPromptInjection = in.ClaudeSystemPromptInjection
	c.ClaudeSystemPrompt = in.ClaudeSystemPrompt
	if len(c.ClaudeSystemPrompt) > 16384 {
		return errors.New("系统提示扩展不能超过 16 KiB")
	}
	if c.ClientVersion != "" && !providers.ValidClientVersion(c.ClientVersion) {
		return errors.New("客户端版本需为 x.y.z 或 x.y.z-alpha.N")
	}
	for _, headers := range []map[string]string{c.GatewayHeaders, c.ModelsHeaders} {
		for k, v := range headers {
			if strings.ContainsAny(k, "\r\n :") || strings.ContainsAny(v, "\r\n") {
				return errors.New("请求头格式无效")
			}
			switch strings.ToLower(k) {
			case "host", "connection", "content-length", "transfer-encoding", "proxy-authorization":
				return errors.New("不能覆盖连接或代理认证请求头")
			}
		}
	}
	if c.Provider == "openai" {
		u, e := url.Parse(c.ModelsURL)
		if e != nil {
			return e
		}
		q := u.Query()
		version := c.ClientVersion
		if version == "" {
			version = providers.DefaultOpenAIClientVersion
		}
		q.Set("client_version", version)
		u.RawQuery = q.Encode()
		c.ModelsURL = u.String()
	}
	if e = c.Validate(); e != nil {
		return e
	}
	encrypted, e := s.seal(id, saved)
	if e != nil {
		return e
	}
	tx, e := s.store.DB.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	result, e := tx.ExecContext(ctx, "UPDATE upstream_accounts SET credentials=?,gateway_platform=?,state='authorized',last_error='',updated_at=? WHERE id=? AND deleted_at IS NULL", encrypted, func() string {
		if c.Gateway != nil {
			return c.Gateway.Platform
		}
		return ""
	}(), time.Now().Unix(), id)
	if e != nil {
		return e
	}
	if n, err := result.RowsAffected(); err != nil {
		return err
	} else if n == 0 {
		return store.ErrNotFound
	}
	if _, e = tx.ExecContext(ctx, "INSERT INTO admin_audit_logs(actor_id,action,target_id,created_at) VALUES(?,'update_account_config',?,?)", actor, id, time.Now().Unix()); e != nil {
		return e
	}
	return tx.Commit()
}
