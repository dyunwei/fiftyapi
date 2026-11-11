package accounts

import (
	"context"
	"encoding/json"
	"errors"
	"fiftyapi/internal/credentials"
	"fiftyapi/internal/providers"
	"time"
)

// Access shares one lock with verification and reauthorization. Token rotation
// is completed and saved in a bounded background job once started: a canceled
// waiting request cannot discard a refresh token already rotated upstream.
func (s *Service) Access(ctx context.Context, id int64) (Credential, error) {
	lock := make(chan struct{}, 1)
	lock <- struct{}{}
	v, _ := s.locks.LoadOrStore(id, lock)
	lock = v.(chan struct{})
	select {
	case <-ctx.Done():
		return Credential{}, ctx.Err()
	case <-lock:
	}
	releaseHere := true
	defer func() {
		if releaseHere {
			lock <- struct{}{}
		}
	}()
	row, err := s.store.Account(ctx, id)
	if err != nil || !row.Enabled {
		return Credential{}, errors.New("账号不可用")
	}
	raw, err := credentials.Decrypt(s.key, binding(id), row.Credentials)
	if err != nil {
		return Credential{}, err
	}
	var c Credential
	if json.Unmarshal(raw, &c) != nil {
		return Credential{}, errors.New("账号凭据无效")
	}
	clear(raw)
	c.Config = providers.NormalizeOpenAIConfig(c.Config)
	if err = s.applyAccountProxy(ctx, row.ProxyID, &c.Config); err != nil {
		return Credential{}, err
	}
	if err = s.ensurePolicy(ctx, id, c.Config.MaxConcurrency); err != nil {
		return Credential{}, err
	}
	c.AccountID = id
	if c.Config.Provider == "gateway" {
		return c, nil
	}
	needsRefresh := time.Until(c.ExpiresAt) <= 3*time.Minute
	needsProject := false
	if c.Config.Provider == "antigravity" {
		var project struct {
			ID string `json:"project"`
		}
		_ = json.Unmarshal(c.Config.ModelsBody, &project)
		needsProject = project.ID == ""
	}
	if !needsRefresh && !needsProject {
		return c, nil
	}
	if needsRefresh && c.Token.RefreshToken == "" {
		_ = s.store.AccountState(ctx, id, "reauth_required", "授权到期且无刷新令牌，请重新授权")
		return Credential{}, errors.New("授权到期，请重新授权")
	}
	if ctx.Err() != nil {
		return Credential{}, ctx.Err()
	}
	type answer struct {
		credential Credential
		err        error
	}
	done := make(chan answer, 1)
	releaseHere = false
	go func() {
		defer func() { lock <- struct{}{} }()
		task, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
		defer cancel()
		current := c
		client := s.client
		if current.Config.ProxyURL != "" || current.Config.TimeoutSeconds != 0 {
			client = providers.HTTPClient(current.Config)
			defer client.CloseIdleConnections()
		}
		if needsRefresh {
			var e error
			current, e = s.refreshAndPersist(task, id, current, client)
			if e != nil {
				done <- answer{err: e}
				return
			}
		}
		if needsProject {
			var e error
			current.Config, e = providers.PrepareAntigravity(task, client, current.Config, current.Token)
			if e != nil {
				done <- answer{err: e}
				return
			}
			encrypted, e := s.seal(id, current)
			if e != nil {
				done <- answer{err: e}
				return
			}
			save, stop := context.WithTimeout(context.WithoutCancel(task), 3*time.Second)
			e = s.store.UpdateAccountCredentials(save, id, encrypted)
			stop()
			if e != nil {
				done <- answer{err: errors.New("项目配置保存失败")}
				return
			}
		}
		done <- answer{credential: current}
	}()
	select {
	case <-ctx.Done():
		return Credential{}, ctx.Err()
	case a := <-done:
		return a.credential, a.err
	}
}
