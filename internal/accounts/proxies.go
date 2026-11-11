package accounts

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"fiftyapi/internal/credentials"
	"fiftyapi/internal/providers"
	"fiftyapi/internal/store"
)

const proxyCheckInterval = 5 * time.Minute

type ProxyInput struct {
	Protocol string `json:"protocol"`
	Host     string `json:"host"`
	Port     int    `json:"port"`
	Location string `json:"location"`
	Note     string `json:"note"`
	Username string `json:"username"`
	Password string `json:"password"`
}

func proxyBinding(id int64) string { return fmt.Sprintf("fiftyapi:proxy:%d", id) }

func (s *Service) sealProxyPassword(password string) func(int64) ([]byte, error) {
	if password == "" {
		return nil
	}
	return func(id int64) ([]byte, error) {
		return credentials.Encrypt(s.key, proxyBinding(id), []byte(password))
	}
}

func (s *Service) decryptProxyPassword(p store.ProxyServer) (string, error) {
	if len(p.Password) == 0 {
		return "", nil
	}
	plain, err := credentials.Decrypt(s.key, proxyBinding(p.ID), p.Password)
	if err != nil {
		return "", errors.New("代理密码无法解密")
	}
	return string(plain), nil
}

func (s *Service) ProxyURL(ctx context.Context, id int64) (string, error) {
	if id <= 0 {
		return "", nil
	}
	p, err := s.store.Proxy(ctx, id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return "", errors.New("所选代理服务器不存在")
		}
		return "", err
	}
	password, err := s.decryptProxyPassword(p)
	if err != nil {
		return "", err
	}
	u := url.URL{
		Scheme: p.Protocol,
		Host:   net.JoinHostPort(p.Host, strconv.Itoa(p.Port)),
	}
	if p.Username != "" || password != "" {
		u.User = url.UserPassword(p.Username, password)
	}
	return u.String(), nil
}

func (s *Service) applyAccountProxy(ctx context.Context, proxyID *int64, cfg *providers.Config) error {
	if proxyID == nil || *proxyID <= 0 {
		return nil
	}
	raw, err := s.ProxyURL(ctx, *proxyID)
	if err != nil {
		return err
	}
	cfg.ProxyURL = raw
	return nil
}

func (s *Service) Proxies(ctx context.Context) ([]store.ProxyServer, error) {
	return s.store.Proxies(ctx)
}

func (s *Service) CreateProxy(ctx context.Context, actor int64, in ProxyInput) (store.ProxyServer, error) {
	if len(in.Password) > 1024 || strings.ContainsAny(in.Password, "\r\n\x00") {
		return store.ProxyServer{}, errors.New("代理密码无效")
	}
	if in.Password != "" && strings.TrimSpace(in.Username) == "" {
		return store.ProxyServer{}, errors.New("填写代理密码时需同时填写代理用户名")
	}
	id, err := s.store.CreateProxy(ctx, actor, store.ProxyServer{
		Protocol: in.Protocol,
		Host:     in.Host,
		Port:     in.Port,
		Location: in.Location,
		Note:     in.Note,
		Username: in.Username,
	}, s.sealProxyPassword(in.Password))
	if err != nil {
		return store.ProxyServer{}, err
	}
	checkCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	checked, err := s.CheckProxy(checkCtx, id)
	if err != nil {
		return s.store.Proxy(ctx, id)
	}
	return checked, nil
}

func (s *Service) UpdateProxy(ctx context.Context, actor, id int64, in ProxyInput) (store.ProxyServer, error) {
	if len(in.Password) > 1024 || strings.ContainsAny(in.Password, "\r\n\x00") {
		return store.ProxyServer{}, errors.New("代理密码无效")
	}
	existing, err := s.store.Proxy(ctx, id)
	if err != nil {
		return store.ProxyServer{}, err
	}
	updatePassword := in.Password != "***"
	effectiveHasPassword := (updatePassword && in.Password != "") || (!updatePassword && existing.HasPassword)
	if effectiveHasPassword && strings.TrimSpace(in.Username) == "" {
		return store.ProxyServer{}, errors.New("填写代理密码时需同时填写代理用户名")
	}
	var seal func(int64) ([]byte, error)
	if updatePassword && in.Password != "" {
		seal = s.sealProxyPassword(in.Password)
	}
	if err := s.store.UpdateProxy(ctx, actor, id, store.ProxyServer{
		Protocol: in.Protocol,
		Host:     in.Host,
		Port:     in.Port,
		Location: in.Location,
		Note:     in.Note,
		Username: in.Username,
	}, updatePassword, seal); err != nil {
		return store.ProxyServer{}, err
	}
	checkCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	checked, err := s.CheckProxy(checkCtx, id)
	if err != nil {
		return s.store.Proxy(ctx, id)
	}
	return checked, nil
}

func (s *Service) DeleteProxy(ctx context.Context, actor, id int64) error {
	return s.store.DeleteProxy(ctx, actor, id)
}

func (s *Service) CheckProxy(ctx context.Context, id int64) (store.ProxyServer, error) {
	p, err := s.store.Proxy(ctx, id)
	if err != nil {
		return store.ProxyServer{}, err
	}
	password, decErr := s.decryptProxyPassword(p)
	var latencyPtr *int
	var checkErr error
	if decErr != nil {
		checkErr = decErr
	} else {
		ms, err := probeProxyServer(ctx, p.Protocol, p.Host, p.Port, p.Username, password)
		if err != nil {
			checkErr = err
		} else {
			latencyPtr = &ms
		}
	}
	errMsg := ""
	if checkErr != nil {
		errMsg = checkErr.Error()
	}
	saveCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
	defer cancel()
	if err := s.store.SaveProxyHealth(saveCtx, id, checkErr == nil, latencyPtr, errMsg); err != nil {
		return store.ProxyServer{}, err
	}
	return s.store.Proxy(saveCtx, id)
}

func (s *Service) CheckAllProxies(ctx context.Context) ([]store.ProxyServer, error) {
	list, err := s.store.Proxies(ctx)
	if err != nil {
		return nil, err
	}
	slots := make(chan struct{}, 4)
	var wg sync.WaitGroup
	for _, item := range list {
		select {
		case <-ctx.Done():
			wg.Wait()
			return nil, ctx.Err()
		case slots <- struct{}{}:
		}
		wg.Add(1)
		go func(id int64) {
			defer wg.Done()
			defer func() { <-slots }()
			task, cancel := context.WithTimeout(ctx, 5*time.Second)
			defer cancel()
			_, _ = s.CheckProxy(task, id)
		}(item.ID)
	}
	wg.Wait()
	return s.store.Proxies(ctx)
}

func (s *Service) MaintainProxies(ctx context.Context) {
	list, err := s.store.Proxies(ctx)
	if err != nil {
		return
	}
	now := time.Now().Unix()
	slots := make(chan struct{}, 4)
	var wg sync.WaitGroup
	for _, item := range list {
		if item.CheckedAt != nil && now-*item.CheckedAt < int64(proxyCheckInterval.Seconds()) {
			continue
		}
		select {
		case <-ctx.Done():
			wg.Wait()
			return
		case slots <- struct{}{}:
		}
		wg.Add(1)
		go func(id int64) {
			defer wg.Done()
			defer func() { <-slots }()
			task, cancel := context.WithTimeout(ctx, 5*time.Second)
			defer cancel()
			_, _ = s.CheckProxy(task, id)
		}(item.ID)
	}
	wg.Wait()
}

func probeProxyServer(ctx context.Context, protocol, host string, port int, username, password string) (int, error) {
	probeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	start := time.Now()
	addr := net.JoinHostPort(host, strconv.Itoa(port))
	var d net.Dialer
	conn, err := d.DialContext(probeCtx, "tcp", addr)
	if err != nil {
		return 0, errors.New("无法连接代理服务器")
	}
	defer conn.Close()
	if dl, ok := probeCtx.Deadline(); ok {
		_ = conn.SetDeadline(dl)
	}
	switch protocol {
	case "https":
		tlsConn := tls.Client(conn, &tls.Config{ServerName: host, MinVersion: tls.VersionTLS12})
		if err := tlsConn.HandshakeContext(probeCtx); err != nil {
			return 0, errors.New("HTTPS 代理 TLS 握手失败")
		}
	case "socks5", "socks5h":
		if username != "" || password != "" {
			if _, err := conn.Write([]byte{0x05, 0x02, 0x00, 0x02}); err != nil {
				return 0, errors.New("SOCKS5 代理握手发送失败")
			}
		} else {
			if _, err := conn.Write([]byte{0x05, 0x01, 0x00}); err != nil {
				return 0, errors.New("SOCKS5 代理握手发送失败")
			}
		}
		var reply [2]byte
		if _, err := io.ReadFull(conn, reply[:]); err != nil || reply[0] != 0x05 {
			return 0, errors.New("SOCKS5 代理握手响应无效")
		}
		switch reply[1] {
		case 0x00:
		case 0x02:
			if username == "" && password == "" {
				return 0, errors.New("SOCKS5 代理需要用户名和密码")
			}
			if len(username) > 255 || len(password) > 255 {
				return 0, errors.New("SOCKS5 认证信息超长")
			}
			auth := make([]byte, 0, 3+len(username)+len(password))
			auth = append(auth, 0x01, byte(len(username)))
			auth = append(auth, username...)
			auth = append(auth, byte(len(password)))
			auth = append(auth, password...)
			if _, err := conn.Write(auth); err != nil {
				return 0, errors.New("SOCKS5 代理认证发送失败")
			}
			var authReply [2]byte
			if _, err := io.ReadFull(conn, authReply[:]); err != nil || authReply[1] != 0x00 {
				return 0, errors.New("SOCKS5 代理用户名或密码错误")
			}
		case 0xFF:
			if username == "" && password == "" {
				return 0, errors.New("SOCKS5 代理需要认证凭据")
			}
			return 0, errors.New("SOCKS5 代理不支持所选认证方式")
		default:
			return 0, errors.New("SOCKS5 代理不支持该认证方式")
		}
	}
	ms := int(time.Since(start).Milliseconds())
	if ms < 0 {
		ms = 0
	}
	return ms, nil
}

func parseProxyURLToInput(raw string, note string) (ProxyInput, bool) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Host == "" {
		return ProxyInput{}, false
	}
	scheme := strings.ToLower(u.Scheme)
	if scheme != "http" && scheme != "https" && scheme != "socks5" && scheme != "socks5h" {
		return ProxyInput{}, false
	}
	host := u.Hostname()
	portStr := u.Port()
	port := 0
	if portStr != "" {
		port, err = strconv.Atoi(portStr)
		if err != nil || port < 1 || port > 65535 {
			return ProxyInput{}, false
		}
	} else {
		switch scheme {
		case "http":
			port = 80
		case "https":
			port = 443
		case "socks5", "socks5h":
			port = 1080
		}
	}
	var username, password string
	if u.User != nil {
		username = u.User.Username()
		password, _ = u.User.Password()
	}
	return ProxyInput{
		Protocol: scheme,
		Host:     host,
		Port:     port,
		Note:     note,
		Username: username,
		Password: password,
	}, true
}

func (s *Service) findOrCreateProxy(ctx context.Context, actor int64, in ProxyInput) (int64, error) {
	existing, err := s.store.Proxies(ctx)
	if err != nil {
		return 0, err
	}
	norm, err := store.NormalizeProxyServer(store.ProxyServer{
		Protocol: in.Protocol,
		Host:     in.Host,
		Port:     in.Port,
		Location: in.Location,
		Note:     in.Note,
		Username: in.Username,
	})
	if err != nil {
		return 0, err
	}
	for _, p := range existing {
		if p.Protocol == norm.Protocol && strings.EqualFold(p.Host, norm.Host) && p.Port == norm.Port && p.Username == norm.Username {
			pw, err := s.decryptProxyPassword(p)
			if err == nil && pw == in.Password {
				return p.ID, nil
			}
		}
	}
	return s.store.CreateProxy(ctx, actor, norm, s.sealProxyPassword(in.Password))
}

func (s *Service) migrateLegacyAccountProxies(ctx context.Context) error {
	rows, err := s.store.Accounts(ctx)
	if err != nil {
		return err
	}
	for _, row := range rows {
		if row.ProxyID != nil {
			continue
		}
		account, err := s.store.Account(ctx, row.ID)
		if err != nil {
			continue
		}
		plain, err := credentials.Decrypt(s.key, binding(row.ID), account.Credentials)
		if err != nil {
			continue
		}
		var c Credential
		if json.Unmarshal(plain, &c) != nil || strings.TrimSpace(c.Config.ProxyURL) == "" {
			continue
		}
		in, ok := parseProxyURLToInput(c.Config.ProxyURL, "自动迁移自账号 #"+strconv.FormatInt(row.ID, 10))
		if !ok {
			continue
		}
		proxyID, err := s.findOrCreateProxy(ctx, 0, in)
		if err != nil || proxyID <= 0 {
			continue
		}
		_, _ = s.store.DB.ExecContext(ctx, `UPDATE upstream_accounts SET proxy_id=? WHERE id=? AND proxy_id IS NULL`, proxyID, row.ID)
	}
	return nil
}
