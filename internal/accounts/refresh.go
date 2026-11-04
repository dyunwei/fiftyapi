package accounts

import (
	"context"
	"encoding/json"
	"errors"
	"fiftyapi/internal/credentials"
	"fiftyapi/internal/providers"
	"net/http"
	"time"
)

func (s *Service) refreshAndPersist(ctx context.Context, id int64, c Credential, client *http.Client) (updated Credential, outcome error) {
	started := time.Now()
	tracked := false
	defer func() {
		s.runtimeOutcome(ctx, id, "token_refresh", "令牌刷新与保存", started, &outcome)
		if tracked {
			s.refreshWG.Done()
		}
	}()
	if c.Token.RefreshToken == "" {
		save, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
		defer cancel()
		_ = s.store.AccountState(save, id, "reauth_required", "没有可用刷新令牌，请重新授权")
		return c, errors.New("账号没有可用刷新令牌，请重新授权")
	}
	// Once the provider may rotate its refresh token, caller cancellation must
	// not abort receiving and persisting the replacement. Shutdown drains this
	// bounded critical section before the shared database is closed.
	s.refreshMu.Lock()
	if s.refreshClosing {
		s.refreshMu.Unlock()
		return c, errors.New("服务正在停止，未开始刷新授权")
	}
	s.refreshWG.Add(1)
	tracked = true
	s.refreshMu.Unlock()
	task, stop := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
	defer stop()
	token, err := providers.Refresh(task, client, c.Config, c.Token, providers.ClientSecret(c.Config))
	if err != nil {
		var upstream *providers.UpstreamHTTPError
		if errors.As(err, &upstream) && (upstream.Code == "invalid_grant" || upstream.Code == "access_denied") {
			save, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
			defer cancel()
			_ = s.store.AccountState(save, id, "reauth_required", "刷新授权已失效，请重新授权")
		}
		return c, errors.New("令牌刷新失败，请检查授权或稍后重试")
	}
	c.Token = token
	c.ExpiresAt = time.Now().Add(time.Duration(token.ExpiresIn) * time.Second)
	encrypted, err := s.seal(id, c)
	if err != nil {
		return c, err
	}
	save, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
	defer cancel()
	if err = s.store.UpdateAccountCredentials(save, id, encrypted); err != nil {
		return c, errors.New("已取得新令牌但保存失败，停止重试以保护刷新轮换")
	}
	return c, nil
}

// RefreshRejected refreshes a rejected access token under the same per-account
// lock used by Access/Verify. A concurrent winner's newer token is reused.
func (s *Service) RefreshRejected(ctx context.Context, id int64, rejected string) error {
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
	row, err := s.store.Account(ctx, id)
	if err != nil || !row.Enabled {
		return errors.New("账号不可用")
	}
	raw, err := credentials.Decrypt(s.key, binding(id), row.Credentials)
	if err != nil {
		return err
	}
	var c Credential
	if json.Unmarshal(raw, &c) != nil {
		return errors.New("账号凭据格式错误")
	}
	if c.Config.Provider == "gateway" {
		return errors.New("API Key 账号需要管理员更新凭据")
	}
	if c.Token.AccessToken != rejected {
		return nil
	}
	client := s.client
	if c.Config.ProxyURL != "" || c.Config.TimeoutSeconds != 0 {
		client = providers.HTTPClient(c.Config)
		defer client.CloseIdleConnections()
	}
	_, err = s.refreshAndPersist(ctx, id, c, client)
	return err
}

type CredentialInfo struct {
	Email            string `json:"email,omitempty"`
	Plan             string `json:"plan,omitempty"`
	AccountHint      string `json:"account_hint,omitempty"`
	ExpiresAt        *int64 `json:"token_expires_at"`
	RefreshAvailable bool   `json:"refresh_available"`
	ClientVersion    string `json:"client_version,omitempty"`
}

func (s *Service) CredentialInfo(ctx context.Context, id int64) (CredentialInfo, error) {
	row, err := s.store.Account(ctx, id)
	if err != nil {
		return CredentialInfo{}, err
	}
	raw, err := credentials.Decrypt(s.key, binding(id), row.Credentials)
	if err != nil {
		return CredentialInfo{}, err
	}
	var c Credential
	if json.Unmarshal(raw, &c) != nil {
		return CredentialInfo{}, errors.New("账号凭据格式错误")
	}
	clear(raw)
	if err = s.ensurePolicy(ctx, id, c.Config.MaxConcurrency); err != nil {
		return CredentialInfo{}, err
	}
	info := CredentialInfo{RefreshAvailable: c.Token.RefreshToken != "", ClientVersion: providers.NormalizeOpenAIConfig(c.Config).ClientVersion}
	if c.Config.Provider == "openai" {
		info.Email, info.Plan = providers.OpenAIIdentity(c.Token)
		account := c.Config.OpenAIAccountID
		if account == "" {
			account = providers.OpenAIAccountID(c.Token)
		}
		if len(account) > 8 {
			info.AccountHint = "…" + account[len(account)-8:]
		} else if account != "" {
			info.AccountHint = "已绑定工作空间"
		}
	}
	if c.Config.Provider != "gateway" {
		n := c.ExpiresAt.Unix()
		info.ExpiresAt = &n
	}
	return info, nil
}

func (s *Service) ensurePolicy(ctx context.Context, id int64, concurrency int) error {
	if _, ok := s.policyChecked.Load(id); ok {
		return nil
	}
	if err := s.store.InitializeAccountPolicy(ctx, id, concurrency); err != nil {
		return err
	}
	s.policyChecked.Store(id, true)
	return nil
}

func (s *Service) Shutdown(ctx context.Context) error {
	s.refreshMu.Lock()
	s.refreshClosing = true
	s.refreshMu.Unlock()
	done := make(chan struct{})
	go func() { s.refreshWG.Wait(); close(done) }()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
