// Package providers contains shared OAuth and upstream protocol adapters.
package providers

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"
)

type Config struct {
	InferencePolicy             InferencePolicy   `json:"inference_policy,omitempty"`
	OpenAIAccountID             string            `json:"openai_account_id,omitempty"`
	Gateway                     *GatewayConfig    `json:"gateway,omitempty"`
	GatewayURL                  string            `json:"gateway_url,omitempty"`
	ProxyURL                    string            `json:"proxy_url,omitempty"`
	GatewayHeaders              map[string]string `json:"gateway_headers,omitempty"`
	UserAgent                   string            `json:"user_agent,omitempty"`
	ClientVersion               string            `json:"client_version,omitempty"`
	MaxConcurrency              int               `json:"max_concurrency,omitempty"`
	TimeoutSeconds              int               `json:"timeout_seconds,omitempty"`
	IdentityPatchEnabled        *bool             `json:"identity_patch_enabled,omitempty"`
	MetadataPassthrough         *bool             `json:"metadata_passthrough,omitempty"`
	FingerprintUnification      *bool             `json:"fingerprint_unification,omitempty"`
	ClaudeSystemPromptInjection *bool             `json:"claude_system_prompt_injection,omitempty"`
	ClaudeSystemPrompt          string            `json:"claude_oauth_system_prompt,omitempty"`
	AntigravityIdentity         string            `json:"antigravity_identity_prompt,omitempty"`
	AntigravityFallbackURLs     []string          `json:"antigravity_fallback_urls,omitempty"`
	AntigravityVersion          string            `json:"antigravity_user_agent_version,omitempty"`
	AllowOnboard                bool              `json:"-"` // Explicit CLI opt-in; never enabled by configuration files.
	Provider                    string            `json:"provider"`
	AuthorizeURL                string            `json:"authorize_url"`
	TokenURL                    string            `json:"token_url"`
	ClientID                    string            `json:"client_id"`
	ClientSecretEnv             string            `json:"client_secret_env,omitempty"`
	RedirectURI                 string            `json:"redirect_uri"`
	Scope                       string            `json:"scope"`
	TokenEncoding               string            `json:"token_encoding"` // form or json
	AuthParams                  map[string]string `json:"auth_params,omitempty"`
	ModelsURL                   string            `json:"models_url"`
	ModelsMethod                string            `json:"models_method,omitempty"`
	ModelsBody                  json.RawMessage   `json:"models_body,omitempty"`
	ModelsHeaders               map[string]string `json:"models_headers,omitempty"`
}

func (c Config) Validate() error {
	if err := c.InferencePolicy.Validate(); err != nil {
		return err
	}
	if c.ClientVersion != "" && !ValidClientVersion(c.ClientVersion) {
		return errors.New("客户端版本格式无效")
	}
	if len(c.UserAgent) > 512 || strings.ContainsAny(c.UserAgent, "\r\n") {
		return errors.New("User-Agent 格式无效")
	}
	if c.OpenAIAccountID != "" && (c.Provider != "openai" || len(c.OpenAIAccountID) > 128 || !regexp.MustCompile(`^[A-Za-z0-9_-]+$`).MatchString(c.OpenAIAccountID)) {
		return errors.New("OpenAI 工作空间账号 ID 无效")
	}

	if c.MaxConcurrency < 0 || c.MaxConcurrency > 100 || c.TimeoutSeconds < 0 || c.TimeoutSeconds > 600 {
		return errors.New("并发或超时配置超出范围")
	}
	if c.GatewayURL != "" {
		u, e := url.Parse(c.GatewayURL)
		if e != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.Fragment != "" {
			return errors.New("网关地址必须为 HTTPS URL")
		}
	}
	if c.ProxyURL != "" {
		u, e := url.Parse(c.ProxyURL)
		if e != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https" && u.Scheme != "socks5" && u.Scheme != "socks5h") {
			return errors.New("代理地址无效")
		}
	}
	for k := range c.GatewayHeaders {
		if strings.EqualFold(k, "Authorization") || strings.EqualFold(k, "Cookie") || strings.EqualFold(k, "x-api-key") {
			return errors.New("自定义请求头不能包含凭据")
		}
	}
	for _, raw := range c.AntigravityFallbackURLs {
		u, err := url.Parse(raw)
		if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
			return errors.New("Antigravity 备用端点必须为 HTTPS 源站地址")
		}
	}
	if c.AntigravityVersion != "" && !regexp.MustCompile(`^\d+\.\d+\.\d+$`).MatchString(c.AntigravityVersion) {
		return errors.New("Antigravity 版本号必须为 x.y.z")
	}
	if c.Provider == "gateway" {
		return c.ValidateGateway()
	}
	if c.Provider == "" || c.ClientID == "" || c.Scope == "" {
		return errors.New("provider、client_id、scope 不能为空")
	}
	for _, raw := range []string{c.AuthorizeURL, c.TokenURL, c.ModelsURL} {
		u, e := url.Parse(raw)
		if e != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.Fragment != "" {
			return errors.New("上游端点必须是无用户信息和 fragment 的 HTTPS URL")
		}
	}
	u, e := url.Parse(c.RedirectURI)
	if e != nil || u.Host == "" || u.User != nil || u.Fragment != "" || (u.Scheme != "https" && !(u.Scheme == "http" && (u.Hostname() == "localhost" || u.Hostname() == "127.0.0.1" || u.Hostname() == "::1"))) {
		return errors.New("redirect_uri 必须为 HTTPS 或本机 HTTP 回调")
	}
	if c.TokenEncoding != "form" && c.TokenEncoding != "json" {
		return errors.New("token_encoding 必须为 form 或 json")
	}
	for k := range c.AuthParams {
		switch k {
		case "state", "client_id", "redirect_uri", "scope", "response_type", "code_challenge", "code_challenge_method":
			return fmt.Errorf("auth_params 不可覆盖 %s", k)
		}
	}
	for k := range c.ModelsHeaders {
		if strings.EqualFold(k, "Authorization") || strings.EqualFold(k, "Cookie") || strings.EqualFold(k, "x-api-key") {
			return errors.New("models_headers 不可包含凭据")
		}
	}
	if c.ModelsMethod != "" && c.ModelsMethod != "GET" && c.ModelsMethod != "POST" {
		return errors.New("models_method 必须为 GET 或 POST")
	}
	if len(c.ModelsBody) > 0 && (!json.Valid(c.ModelsBody) || c.ModelsMethod != "POST") {
		return errors.New("models_body 需要有效 JSON 及 POST 方法")
	}
	return nil
}

type Session struct {
	mu              sync.Mutex
	state, verifier string
	expires         time.Time
	consumed        bool
}

func randomString() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func Start(c Config) (*Session, string, error) {
	if err := c.Validate(); err != nil {
		return nil, "", err
	}
	state, err := randomString()
	if err != nil {
		return nil, "", err
	}
	verifier, err := randomString()
	if err != nil {
		return nil, "", err
	}
	s := &Session{state: state, verifier: verifier, expires: time.Now().Add(10 * time.Minute)}
	hash := sha256.Sum256([]byte(verifier))
	u, _ := url.Parse(c.AuthorizeURL)
	q := u.Query()
	for k, v := range c.AuthParams {
		q.Set(k, v)
	}
	for k, v := range map[string]string{"client_id": c.ClientID, "redirect_uri": c.RedirectURI, "scope": c.Scope, "response_type": "code", "state": state, "code_challenge": base64.RawURLEncoding.EncodeToString(hash[:]), "code_challenge_method": "S256"} {
		q.Set(k, v)
	}
	u.RawQuery = q.Encode()
	return s, u.String(), nil
}

// consume validates before consuming, then forbids retries even if exchange fails.
func (s *Session) consume(c Config, result string) (url.Values, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.consumed || !time.Now().Before(s.expires) {
		return nil, errors.New("授权会话已过期或已使用")
	}
	var code, state string
	if u, e := url.Parse(result); e == nil && u.IsAbs() {
		want, _ := url.Parse(c.RedirectURI)
		if u.Scheme != want.Scheme || u.Host != want.Host || u.Path != want.Path || u.User != nil || u.Fragment != "" {
			return nil, errors.New("回调地址不匹配")
		}
		q := u.Query()
		if len(q["code"]) != 1 || len(q["state"]) != 1 || q.Get("error") != "" {
			return nil, errors.New("回调缺少唯一 code/state 或授权被拒绝")
		}
		code, state = q.Get("code"), q.Get("state")
	} else {
		var ok bool
		code, state, ok = strings.Cut(result, "#")
		if !ok {
			return nil, errors.New("需要完整回调 URL 或 code#state，不能仅提交 code")
		}
	}
	if code == "" || subtle.ConstantTimeCompare([]byte(state), []byte(s.state)) != 1 {
		return nil, errors.New("授权 state 不匹配")
	}
	s.consumed = true
	return url.Values{"grant_type": {"authorization_code"}, "client_id": {c.ClientID}, "redirect_uri": {c.RedirectURI}, "code": {code}, "state": {state}, "code_verifier": {s.verifier}}, nil
}

type Token struct {
	Account      *OAuthAccount `json:"account,omitempty"`
	IDToken      string        `json:"id_token,omitempty"`
	AccessToken  string        `json:"access_token"`
	RefreshToken string        `json:"refresh_token"`
	ExpiresIn    int64         `json:"expires_in"`
}
type OAuthAccount struct {
	UUID string `json:"uuid"`
}

// NewClient refuses redirects so credentials never follow another endpoint.
func NewClient() *http.Client {
	return &http.Client{Timeout: 45 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
}

func Exchange(ctx context.Context, client *http.Client, c Config, s *Session, result, secret string) (Token, error) {
	v, err := s.consume(c, result)
	if err != nil {
		return Token{}, err
	}
	return tokenRequest(ctx, client, c, v, secret)
}

func Refresh(ctx context.Context, client *http.Client, c Config, t Token, secret string) (Token, error) {
	if t.RefreshToken == "" {
		return Token{}, errors.New("供应商没有返回 refresh_token")
	}
	next, err := tokenRequest(ctx, client, c, url.Values{"grant_type": {"refresh_token"}, "client_id": {c.ClientID}, "refresh_token": {t.RefreshToken}}, secret)
	if err == nil && next.RefreshToken == "" {
		next.RefreshToken = t.RefreshToken
	}
	if err == nil {
		if next.Account == nil {
			next.Account = t.Account
		}
		if next.IDToken == "" {
			next.IDToken = t.IDToken
		}
	}
	return next, err
}

func tokenRequest(ctx context.Context, client *http.Client, c Config, v url.Values, secret string) (Token, error) {
	if secret != "" {
		v.Set("client_secret", secret)
	}
	body := []byte(v.Encode())
	ct := "application/x-www-form-urlencoded"
	if c.TokenEncoding == "json" {
		m := map[string]string{}
		for k := range v {
			m[k] = v.Get(k)
		}
		body, _ = json.Marshal(m)
		ct = "application/json"
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.TokenURL, bytes.NewReader(body))
	if err != nil {
		return Token{}, errors.New("无法构造换码请求")
	}
	req.Header.Set("Content-Type", ct)
	IdentityHeaders(req.Header, c)
	if c.Provider == "openai" {
		req.Header.Del("version")
	}
	req.Header.Set("Accept", "application/json")
	data, err := perform(client, req)
	if err != nil {
		var upstream *UpstreamHTTPError
		defaults, _ := DefaultConfig("antigravity")
		if c.Provider == "antigravity" && c.ClientID == defaults.ClientID && secret != "" && secret != antigravityDesktopClientSecret && errors.As(err, &upstream) && upstream.Code == "invalid_client" {
			name := c.ClientSecretEnv
			if name == "" {
				name = "ANTIGRAVITY_OAUTH_CLIENT_SECRET"
			}
			return Token{}, fmt.Errorf("%w；当前服务使用了 %s 环境变量覆盖内置客户端参数。默认客户端请清空该变量并重启服务，再重新生成授权链接（未输出配置值）", err, name)
		}
		return Token{}, err
	}
	var t Token
	if json.Unmarshal(data, &t) != nil || t.AccessToken == "" {
		return Token{}, errors.New("令牌响应缺少有效 access_token")
	}
	return t, nil
}

const bodyLimit = 8 << 20

type UpstreamHTTPError struct {
	Status int
	Code   string
}

func (e *UpstreamHTTPError) Error() string {
	if e.Code != "" {
		return fmt.Sprintf("上游 HTTP %d；错误码 %s%s（原始响应未输出）", e.Status, e.Code, errorHint(e.Code))
	}
	return fmt.Sprintf("上游 HTTP %d（响应内容未输出）", e.Status)
}

type upstreamNetworkError struct{ cause error }

func (e *upstreamNetworkError) Error() string {
	return "上游网络请求失败（已隐藏 URL 与响应内容）"
}
func (e *upstreamNetworkError) Unwrap() error { return e.cause }

func perform(client *http.Client, req *http.Request) ([]byte, error) {
	resp, err := client.Do(req)
	if err != nil {
		return nil, &upstreamNetworkError{cause: err}
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		// Only emit known protocol codes, never arbitrary upstream strings:
		// error descriptions can echo credentials or authorization codes.
		body, readErr := io.ReadAll(io.LimitReader(resp.Body, bodyLimit+1))
		if readErr == nil && len(body) <= bodyLimit {
			if code := safeErrorCode(body); code != "" {
				return nil, &UpstreamHTTPError{Status: resp.StatusCode, Code: code}
			}
		}
		return nil, &UpstreamHTTPError{Status: resp.StatusCode}
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, bodyLimit+1))
	if err != nil {
		return nil, errors.New("读取上游响应失败")
	}
	if len(b) > bodyLimit {
		return nil, errors.New("上游响应超过大小限制")
	}
	return b, nil
}

func safeErrorCode(body []byte) string {
	var response struct {
		Error json.RawMessage `json:"error"`
	}
	if json.Unmarshal(body, &response) != nil {
		return ""
	}
	var code string
	if json.Unmarshal(response.Error, &code) != nil {
		var object struct {
			Status string `json:"status"`
		}
		if json.Unmarshal(response.Error, &object) != nil {
			return ""
		}
		code = object.Status
	}
	switch code {
	case "invalid_client", "invalid_grant", "invalid_request", "invalid_scope", "unauthorized_client", "access_denied", "unsupported_grant_type", "temporarily_unavailable", "UNAUTHENTICATED", "PERMISSION_DENIED", "RESOURCE_EXHAUSTED":
		return code
	}
	return ""
}

func errorHint(code string) string {
	switch code {
	case "invalid_client":
		return "（请核对配套的 client_id/client_secret）"
	case "invalid_grant":
		return "（请重新授权，勿复用旧回调，并核对 redirect_uri 与 PKCE 会话）"
	case "UNAUTHENTICATED":
		return "（上游未接受当前访问令牌）"
	case "PERMISSION_DENIED", "access_denied":
		return "（上游拒绝了访问权限）"
	}
	return ""
}
