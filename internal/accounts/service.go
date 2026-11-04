package accounts

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"fiftyapi/internal/credentials"
	"fiftyapi/internal/providers"
	"fiftyapi/internal/store"
)

type Credential struct {
	APIKey    string           `json:"api_key,omitempty"`
	AccountID int64            `json:"-"`
	Config    providers.Config `json:"config"`
	Token     providers.Token  `json:"token"`
	ExpiresAt time.Time        `json:"expires_at"`
}
type Service struct {
	store          *store.Store
	key            []byte
	client         *http.Client
	locks          sync.Map
	oauthMu        sync.Mutex
	oauth          map[string]*oauthPending
	checked        sync.Map
	policyChecked  sync.Map
	refreshMu      sync.Mutex
	refreshWG      sync.WaitGroup
	refreshClosing bool
	relayID        string
}

type Option func(*Service)

func WithHTTPClient(client *http.Client) Option { return func(s *Service) { s.client = client } }
func New(s *store.Store, key []byte, options ...Option) (*Service, error) {
	if len(key) != 32 {
		return nil, errors.New("账号主密钥必须为 32 字节")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := s.CheckMasterKey(ctx, key); err != nil {
		return nil, err
	}
	relayID, err := s.RelayInstanceID(ctx)
	if err != nil {
		return nil, err
	}
	service := &Service{relayID: relayID, store: s, key: append([]byte(nil), key...), client: providers.NewClient(), oauth: map[string]*oauthPending{}}
	for _, option := range options {
		option(service)
	}
	return service, nil
}

// Keep the stored AAD stable across the FiftyAPI rename; old account ciphertext
// must remain decryptable with the original master key.
func binding(id int64) string { return fmt.Sprintf("ztokenmgr:upstream:%d", id) }
func (s *Service) Import(ctx context.Context, actor int64, name string, c Credential) (int64, error) {
	return s.importWithOrigin(ctx, actor, name, c, store.AccountOrigin{})
}
func (s *Service) importWithOrigin(ctx context.Context, actor int64, name string, c Credential, origin store.AccountOrigin) (int64, error) {
	if strings.TrimSpace(name) == "" || len(name) > 128 || (c.Config.Provider != "antigravity" && c.Config.Provider != "claude" && c.Config.Provider != "openai" && c.Config.Provider != "gateway") || (c.Config.Provider != "gateway" && (c.Token.AccessToken == "" || c.ExpiresAt.IsZero())) {
		return 0, errors.New("账号或授权信息不完整")
	}
	if c.Config.Provider == "gateway" && (len(c.APIKey) < 1 || len(c.APIKey) > 4096 || strings.TrimSpace(c.APIKey) != c.APIKey || strings.ContainsAny(c.APIKey, "\r\n")) {
		return 0, errors.New("API Key 无效")
	}
	if err := c.Config.Validate(); err != nil {
		return 0, err
	}
	plain, err := json.Marshal(c)
	if err != nil {
		return 0, err
	}
	platform := ""
	if c.Config.Gateway != nil {
		platform = c.Config.Gateway.Platform
	}
	id, err := s.store.CreateAccountWithOrigin(ctx, actor, name, c.Config.Provider, func(id int64) ([]byte, error) { return credentials.Encrypt(s.key, binding(id), plain) }, platform, origin)
	if err != nil {
		return 0, err
	}
	if err = s.ensurePolicy(ctx, id, c.Config.MaxConcurrency); err != nil {
		return id, err
	}
	return id, nil
}
func (s *Service) seal(id int64, c Credential) ([]byte, error) {
	b, err := json.Marshal(c)
	if err != nil {
		return nil, err
	}
	return credentials.Encrypt(s.key, binding(id), b)
}

// Verify serializes refresh and project backfill per account. No SQLite write
// transaction is held during upstream calls. Successful refresh is saved before
// later discovery so a project failure cannot discard a rotated refresh token.
func (s *Service) Verify(ctx context.Context, id int64) ([]string, error) {
	return s.verify(ctx, id, false)
}
func (s *Service) verify(ctx context.Context, id int64, allowOnboard bool) (modelIDs []string, outcome error) {
	defer s.runtimeOutcome(ctx, id, "model_sync", "账号验证与模型同步", time.Now(), &outcome)
	lock := make(chan struct{}, 1)
	lock <- struct{}{}
	value, _ := s.locks.LoadOrStore(id, lock)
	lock = value.(chan struct{})
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-lock:
	}
	defer func() { lock <- struct{}{} }()
	account, err := s.store.Account(ctx, id)
	if err != nil {
		return nil, errors.New("上游账号不存在")
	}
	if !account.Enabled {
		return nil, errors.New("上游账号已停用")
	}
	plain, err := credentials.Decrypt(s.key, binding(id), account.Credentials)
	if err != nil {
		return nil, err
	}
	var c Credential
	if json.Unmarshal(plain, &c) != nil {
		return nil, errors.New("账号凭据格式错误")
	}
	c.Config = providers.NormalizeOpenAIConfig(c.Config)
	if err = s.ensurePolicy(ctx, id, c.Config.MaxConcurrency); err != nil {
		return nil, err
	}
	client := s.client
	if c.Config.ProxyURL != "" || c.Config.TimeoutSeconds != 0 {
		client = providers.HTTPClient(c.Config)
		defer client.CloseIdleConnections()
	}
	catalogPhase := false
	fail := func(state string, e error) ([]string, error) {
		// A request may be canceled; status persistence uses a separate bounded context.
		saveCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
		defer cancel()
		if catalogPhase && (account.State == "catalog_ready" || account.State == "ready") {
			var upstream *providers.UpstreamHTTPError
			if !errors.As(e, &upstream) || (upstream.Status != 401 && upstream.Status != 403) {
				state = account.State
			}
		}
		if persistErr := s.store.AccountState(saveCtx, id, state, e.Error()); persistErr != nil {
			return nil, errors.New("账号验证失败，且状态保存失败")
		}
		return nil, e
	}
	if c.Config.Provider == "gateway" {
		c.Config.Gateway.BaseURL, err = providers.NormalizeGatewayBase(c.Config.Gateway.BaseURL, c.Config.Gateway.AllowHTTP)
		if err != nil {
			return fail("error", err)
		}
		client := s.client
		if c.Config.ProxyURL != "" || c.Config.TimeoutSeconds != 0 {
			client = providers.HTTPClient(c.Config)
			defer client.CloseIdleConnections()
		}
		catalogPhase = true
		mappings, e := c.Config.GatewayModels(ctx, client, c.APIKey, s.relayID)
		if e != nil {
			return fail("error", e)
		}
		names, e := s.store.SyncGatewayModels(ctx, id, mappings)
		if e != nil {
			return fail("error", e)
		}
		if e = s.store.AccountState(ctx, id, "catalog_ready", ""); e != nil {
			return nil, e
		}
		s.checked.Store(id, time.Now())
		return names, nil
	}
	if time.Until(c.ExpiresAt) <= 3*time.Minute {
		if c.Token.RefreshToken == "" {
			return fail("reauth_required", errors.New("授权到期且无刷新令牌，请重新授权"))
		}
		secret := providers.ClientSecret(c.Config)
		refreshStart := time.Now()
		token, e := providers.Refresh(ctx, client, c.Config, c.Token, secret)
		s.runtimeOutcome(ctx, id, "token_refresh", "令牌刷新", refreshStart, &e)
		if e != nil {
			state := "error"
			var httpErr *providers.UpstreamHTTPError
			if errors.As(e, &httpErr) && (httpErr.Code == "invalid_grant" || httpErr.Code == "access_denied") {
				state = "reauth_required"
			}
			return fail(state, e)
		}
		c.Token = token
		c.ExpiresAt = time.Now().Add(time.Duration(token.ExpiresIn) * time.Second)
		encrypted, e := s.seal(id, c)
		if e != nil {
			return nil, e
		}
		saveCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
		defer cancel()
		if e = s.store.UpdateAccountCredentials(saveCtx, id, encrypted); e != nil {
			return nil, errors.New("令牌已刷新，但无法保存；请检查数据库后重新授权")
		}
	}
	// Import does not grant permission to initialize accounts automatically.
	c.Config.AllowOnboard = allowOnboard
	if c.Config.Provider == "antigravity" {
		c.Config, err = providers.PrepareAntigravity(ctx, client, c.Config, c.Token)
	}
	if err != nil {
		var pending *providers.ProjectPendingError
		if errors.As(err, &pending) {
			return fail("project_pending", err)
		}
		return fail("error", err)
	}
	encrypted, err := s.seal(id, c)
	if err != nil {
		return nil, err
	}
	if err = s.store.UpdateAccountCredentials(ctx, id, encrypted); err != nil {
		return nil, errors.New("保存项目配置失败")
	}
	catalogPhase = true
	models, err := providers.Models(ctx, client, c.Config, c.Token)
	var catalogHTTP *providers.UpstreamHTTPError
	if errors.As(err, &catalogHTTP) && catalogHTTP.Status == 401 && c.Token.RefreshToken != "" {
		refreshed, refreshErr := s.refreshAndPersist(ctx, id, c, client)
		if refreshErr != nil {
			return nil, refreshErr
		}
		c = refreshed
		models, err = providers.Models(ctx, client, c.Config, c.Token)
	}
	if err != nil {
		return fail("error", err)
	}
	if err = s.store.SyncModels(ctx, id, c.Config.Provider, models); err != nil {
		return nil, errors.New("保存模型目录失败")
	}
	if err = s.store.AccountState(ctx, id, "catalog_ready", ""); err != nil {
		return nil, errors.New("保存账号状态失败")
	}
	s.checked.Store(id, time.Now())
	return models, nil
}
