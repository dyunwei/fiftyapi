package accounts

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fiftyapi/internal/providers"
	"fiftyapi/internal/store"
	"strconv"
	"strings"
	"time"
)

// ImportAuth accepts token-only files. It never trusts file-supplied endpoints,
// proxies, headers, roles, or gateway configuration as OAuth client defaults.
func (s *Service) ImportAuth(ctx context.Context, actor int64, name, provider string, raw json.RawMessage, proxyID ...int64) (int64, error) {
	return s.importAuth(ctx, actor, name, provider, raw, 0, proxyID...)
}
func (s *Service) ImportContributedAuth(ctx context.Context, actor int64, name, provider string, raw json.RawMessage, linkID int64, proxyID ...int64) (int64, error) {
	if linkID <= 0 {
		return 0, errors.New("贡献链接无效")
	}
	return s.importAuth(ctx, actor, name, provider, raw, linkID, proxyID...)
}
func (s *Service) importAuth(ctx context.Context, actor int64, name, provider string, raw json.RawMessage, linkID int64, proxyID ...int64) (int64, error) {
	c, fingerprint, err := ParseAuth(provider, raw)
	if err != nil {
		return 0, err
	}
	origin := store.AccountOrigin{LinkID: linkID, Fingerprint: fingerprint}
	if len(proxyID) > 0 {
		if proxyID[0] < 0 {
			return 0, errors.New("所选代理服务器不存在")
		}
		if proxyID[0] > 0 {
			pid := proxyID[0]
			origin.ProxyID = &pid
		}
	}
	return s.importWithOrigin(ctx, actor, name, c, origin)
}

// ParseAuth validates authorization fields without persisting or contacting upstreams.
func ParseAuth(provider string, raw json.RawMessage) (Credential, []byte, error) {
	cfg, err := providers.DefaultConfig(provider)
	if err != nil {
		return Credential{}, nil, err
	}
	var root map[string]json.RawMessage
	if json.Unmarshal(raw, &root) != nil || root == nil {
		return Credential{}, nil, errors.New("授权文件不是 JSON 对象")
	}
	source := raw
	if nested, ok := root["tokens"]; ok {
		source = nested
	} else if nested, ok := root["credentials"]; ok {
		source = nested
	}
	var fields struct {
		Access           string          `json:"access_token"`
		Refresh          string          `json:"refresh_token"`
		ID               string          `json:"id_token"`
		AccountID        string          `json:"account_id"`
		ChatGPTAccountID string          `json:"chatgpt_account_id"`
		ClientID         string          `json:"client_id"`
		ExpiresIn        int64           `json:"expires_in"`
		ExpiresAt        json.RawMessage `json:"expires_at"`
	}
	if json.Unmarshal(source, &fields) != nil {
		return Credential{}, nil, errors.New("授权字段格式无效")
	}
	// Claude Code's native credential file uses camelCase fields and a
	// millisecond timestamp inside claudeAiOauth. Only extract OAuth fields.
	if native, ok := root["claudeAiOauth"]; ok && provider == "claude" {
		var oauth struct {
			Access    string `json:"accessToken"`
			Refresh   string `json:"refreshToken"`
			ExpiresAt int64  `json:"expiresAt"`
		}
		if json.Unmarshal(native, &oauth) != nil {
			return Credential{}, nil, errors.New("Claude 授权字段格式无效")
		}
		fields.Access, fields.Refresh = oauth.Access, oauth.Refresh
		if oauth.ExpiresAt > 0 {
			fields.ExpiresAt = json.RawMessage(strconv.FormatInt(oauth.ExpiresAt/1000, 10))
		}
	}
	for _, v := range []string{fields.Access, fields.Refresh, fields.ID} {
		if len(v) > 32<<10 || strings.ContainsAny(v, "\r\n") {
			return Credential{}, nil, errors.New("授权字段超出限制或包含换行")
		}
	}
	if fields.Access == "" && fields.Refresh == "" {
		return Credential{}, nil, errors.New("文件缺少访问令牌或刷新令牌")
	}
	expires := time.Time{}
	if len(fields.ExpiresAt) > 0 && string(fields.ExpiresAt) != "null" {
		var seconds int64
		if json.Unmarshal(fields.ExpiresAt, &seconds) == nil && seconds > 0 {
			expires = time.Unix(seconds, 0)
		} else {
			var text string
			if json.Unmarshal(fields.ExpiresAt, &text) == nil {
				if n, e := strconv.ParseInt(text, 10, 64); e == nil && n > 0 {
					expires = time.Unix(n, 0)
				} else {
					expires, _ = time.Parse(time.RFC3339Nano, text)
				}
			}
		}
	}
	if expires.IsZero() {
		parts := strings.Split(fields.Access, ".")
		if len(parts) > 1 {
			b, e := base64.RawURLEncoding.DecodeString(parts[1])
			if e == nil {
				var claims struct {
					Exp int64 `json:"exp"`
				}
				if json.Unmarshal(b, &claims) == nil && claims.Exp > 0 {
					expires = time.Unix(claims.Exp, 0)
				}
			}
		}
	}
	if expires.IsZero() && fields.ExpiresIn > 0 && fields.ExpiresIn <= 365*86400 {
		expires = time.Now().Add(time.Duration(fields.ExpiresIn) * time.Second)
	}
	if fields.Access == "" {
		fields.Access = "pending-oauth-refresh"
		expires = time.Now().Add(-time.Second)
	}
	if expires.IsZero() {
		if fields.Refresh == "" {
			return Credential{}, nil, errors.New("无法确定授权有效期，请提供 expires_at 或可刷新授权")
		}
		expires = time.Now().Add(-time.Second)
	}
	if fields.ClientID != "" {
		if len(fields.ClientID) > 256 || strings.ContainsAny(fields.ClientID, "\r\n ") {
			return Credential{}, nil, errors.New("OAuth client_id 无效")
		}
		cfg.ClientID = fields.ClientID
	}
	if provider == "openai" {
		cfg.OpenAIAccountID = fields.ChatGPTAccountID
		if cfg.OpenAIAccountID == "" {
			cfg.OpenAIAccountID = fields.AccountID
		}
	}
	c := Credential{Config: cfg, Token: providers.Token{AccessToken: fields.Access, RefreshToken: fields.Refresh, IDToken: fields.ID}, ExpiresAt: expires}
	return c, authFingerprint(c), nil
}

func authFingerprint(c Credential) []byte {
	identity := c.Token.RefreshToken
	if identity == "" {
		identity = c.Token.AccessToken
	}
	if c.Config.OpenAIAccountID != "" {
		identity = "account:" + c.Config.OpenAIAccountID
	}
	fingerprint := sha256.Sum256([]byte(c.Config.Provider + "\x00" + identity))
	return fingerprint[:]
}
