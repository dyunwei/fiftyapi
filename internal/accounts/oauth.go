package accounts

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fiftyapi/internal/credentials"
	"fiftyapi/internal/providers"
	"fiftyapi/internal/store"
	"strings"
	"time"
)

type oauthPending struct {
	linkID  int64
	proxyID *int64
	owner   int64
	name    string
	target  int64
	config  providers.Config
	session *providers.Session
	expires time.Time
}
type OAuthStart struct {
	SessionID   string `json:"session_id"`
	AuthURL     string `json:"auth_url"`
	RedirectURI string `json:"redirect_uri"`
	ExpiresAt   int64  `json:"expires_at"`
}

func (s *Service) StartOAuth(ctx context.Context, actor int64, provider, name string, target int64, proxyID ...int64) (OAuthStart, error) {
	return s.startOAuth(ctx, actor, provider, name, target, 0, proxyID...)
}

func (s *Service) StartContributedOAuth(ctx context.Context, actor int64, provider, name string, linkID int64, proxyID ...int64) (OAuthStart, error) {
	if linkID <= 0 {
		return OAuthStart{}, errors.New("贡献链接无效")
	}
	return s.startOAuth(ctx, actor, provider, name, 0, linkID, proxyID...)
}

func (s *Service) startOAuth(ctx context.Context, actor int64, provider, name string, target, linkID int64, proxyID ...int64) (OAuthStart, error) {
	name = strings.TrimSpace(name)
	c, err := providers.DefaultConfig(provider)
	if err != nil {
		return OAuthStart{}, err
	}
	var chosenProxyID *int64
	if target > 0 {
		row, e := s.store.Account(ctx, target)
		if e != nil || row.Provider != provider {
			return OAuthStart{}, errors.New("重新授权账号不存在或平台不匹配")
		}
		name = row.Name
		plain, e := credentials.Decrypt(s.key, binding(target), row.Credentials)
		if e != nil {
			return OAuthStart{}, e
		}
		var saved Credential
		if json.Unmarshal(plain, &saved) != nil {
			return OAuthStart{}, errors.New("账号配置无效")
		}
		c = saved.Config
		chosenProxyID = row.ProxyID
	}
	if len(proxyID) > 0 {
		if proxyID[0] < 0 {
			return OAuthStart{}, errors.New("所选代理服务器不存在")
		}
		if proxyID[0] > 0 {
			pid := proxyID[0]
			chosenProxyID = &pid
		} else {
			chosenProxyID = nil
			c.ProxyURL = ""
		}
	}
	if chosenProxyID != nil {
		if err = s.applyAccountProxy(ctx, chosenProxyID, &c); err != nil {
			return OAuthStart{}, err
		}
	}
	if name == "" || len(name) > 128 {
		return OAuthStart{}, errors.New("账号名称需为 1–128 字节")
	}
	if target == 0 {
		var n int
		if err = s.store.DB.QueryRowContext(ctx, "SELECT count(*) FROM upstream_accounts WHERE name=?", name).Scan(&n); err != nil {
			return OAuthStart{}, err
		}
		if n > 0 {
			return OAuthStart{}, errors.New("账号名称已存在")
		}
	}
	session, authURL, err := providers.Start(c)
	if err != nil {
		return OAuthStart{}, err
	}
	b := make([]byte, 32)
	if _, err = rand.Read(b); err != nil {
		return OAuthStart{}, err
	}
	id := base64.RawURLEncoding.EncodeToString(b)
	now := time.Now()
	s.oauthMu.Lock()
	defer s.oauthMu.Unlock()
	for key, value := range s.oauth {
		if !now.Before(value.expires) {
			delete(s.oauth, key)
		}
	}
	if len(s.oauth) >= 64 {
		return OAuthStart{}, errors.New("未完成授权过多，请稍后重试")
	}
	expires := now.Add(10 * time.Minute)
	s.oauth[id] = &oauthPending{linkID: linkID, proxyID: chosenProxyID, owner: actor, name: name, target: target, config: c, session: session, expires: expires}
	return OAuthStart{SessionID: id, AuthURL: authURL, RedirectURI: c.RedirectURI, ExpiresAt: expires.Unix()}, nil
}
func (s *Service) CompleteOAuth(ctx context.Context, actor int64, id, result string, onboard bool) (int64, string, error) {
	return s.completeOAuth(ctx, actor, id, result, onboard, 0)
}

func (s *Service) CompleteContributedOAuth(ctx context.Context, actor int64, id, result string, linkID int64) (int64, error) {
	if linkID <= 0 {
		return 0, errors.New("贡献链接无效")
	}
	account, _, err := s.completeOAuth(ctx, actor, id, result, false, linkID)
	return account, err
}

func (s *Service) completeOAuth(ctx context.Context, actor int64, id, result string, onboard bool, linkID int64) (int64, string, error) {
	s.oauthMu.Lock()
	pending, ok := s.oauth[id]
	if ok && pending.owner == actor && pending.linkID == linkID {
		delete(s.oauth, id)
	}
	s.oauthMu.Unlock()
	if !ok || pending.owner != actor || pending.linkID != linkID || !time.Now().Before(pending.expires) {
		return 0, "", errors.New("授权会话已过期、已使用或不属于当前管理员")
	}
	client := s.client
	if pending.config.ProxyURL != "" || pending.config.TimeoutSeconds != 0 {
		client = providers.HTTPClient(pending.config)
		defer client.CloseIdleConnections()
	}
	token, err := providers.Exchange(ctx, client, pending.config, pending.session, result, providers.ClientSecret(pending.config))
	if err != nil {
		return 0, "", err
	}
	c := Credential{Config: pending.config, Token: token, ExpiresAt: time.Now().Add(time.Duration(token.ExpiresIn) * time.Second)}
	if linkID > 0 {
		accountID, err := s.importWithOrigin(ctx, actor, pending.name, c, store.AccountOrigin{LinkID: linkID, ProxyID: pending.proxyID, Fingerprint: authFingerprint(c)})
		return accountID, "", err
	}
	accountID := pending.target
	if accountID == 0 {
		accountID, err = s.importWithOrigin(ctx, actor, pending.name, c, store.AccountOrigin{ProxyID: pending.proxyID})
	} else {
		err = func() error {
			lock := make(chan struct{}, 1)
			lock <- struct{}{}
			v, _ := s.locks.LoadOrStore(accountID, lock)
			lock = v.(chan struct{})
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-lock:
			}
			defer func() { lock <- struct{}{} }()
			row, e := s.store.Account(ctx, accountID)
			if e != nil {
				return e
			}
			plain, e := credentials.Decrypt(s.key, binding(accountID), row.Credentials)
			if e != nil {
				return e
			}
			var current Credential
			if json.Unmarshal(plain, &current) != nil {
				return errors.New("当前账号配置无效")
			}
			c.Config = current.Config
			var proxyIDVal any
			if pending.proxyID != nil && *pending.proxyID > 0 {
				if e = s.applyAccountProxy(ctx, pending.proxyID, &c.Config); e != nil {
					return e
				}
				proxyIDVal = *pending.proxyID
			} else if row.ProxyID != nil && pending.proxyID == nil {
				c.Config.ProxyURL = ""
				proxyIDVal = nil
			}
			encrypted, e := s.seal(accountID, c)
			if e != nil {
				return e
			}
			if _, e = s.store.DB.ExecContext(ctx, "UPDATE upstream_accounts SET credentials=?,proxy_id=?,updated_at=? WHERE id=? AND deleted_at IS NULL", encrypted, proxyIDVal, time.Now().Unix(), accountID); e != nil {
				return e
			}
			return s.store.AccountState(ctx, accountID, "authorized", "")
		}()
	}
	if err != nil {
		return 0, "", errors.New("授权成功但无法保存账号，请检查账号名或数据库")
	}
	_, err = s.verify(ctx, accountID, onboard)
	if err != nil {
		return accountID, err.Error(), nil
	} // Preserve successful authorization.
	return accountID, "", nil
}
