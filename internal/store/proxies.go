package store

import (
	"context"
	"database/sql"
	"errors"
	"net"
	"net/url"
	"strconv"
	"strings"
	"time"
)

var ErrProxyInUse = errors.New("该代理仍被上游账号使用，请先在相关账号中解绑代理")

type ProxyServer struct {
	ID           int64  `json:"id"`
	Protocol     string `json:"protocol"`
	Host         string `json:"host"`
	Port         int    `json:"port"`
	Location     string `json:"location"`
	Note         string `json:"note"`
	Username     string `json:"username"`
	HasPassword  bool   `json:"has_password"`
	Password     []byte `json:"-"`
	Healthy      *bool  `json:"healthy"`
	LatencyMS    *int   `json:"latency_ms"`
	LastError    string `json:"last_error,omitempty"`
	CheckedAt    *int64 `json:"checked_at"`
	AccountCount int    `json:"account_count"`
	CreatedAt    int64  `json:"created_at"`
	UpdatedAt    int64  `json:"updated_at"`
}

type ContributionProxy struct {
	ID       int64  `json:"id"`
	Location string `json:"location"`
	Note     string `json:"note"`
	Healthy  *bool  `json:"healthy"`
}

func NormalizeProxyServer(p ProxyServer) (ProxyServer, error) {
	p.Protocol = strings.ToLower(strings.TrimSpace(p.Protocol))
	if p.Protocol == "" {
		p.Protocol = "http"
	}
	if p.Protocol != "http" && p.Protocol != "https" && p.Protocol != "socks5" && p.Protocol != "socks5h" {
		return p, errors.New("代理协议无效，仅支持 HTTP、HTTPS、SOCKS5")
	}
	p.Host = strings.TrimSpace(p.Host)
	if strings.HasPrefix(p.Host, "[") && strings.HasSuffix(p.Host, "]") {
		p.Host = strings.TrimPrefix(strings.TrimSuffix(p.Host, "]"), "[")
	}
	if p.Host == "" || len(p.Host) > 255 || strings.ContainsAny(p.Host, "/@?# \t\r\n\x00") || strings.Contains(p.Host, "://") {
		return p, errors.New("代理服务器地址无效")
	}
	if strings.Contains(p.Host, ":") && net.ParseIP(p.Host) == nil {
		return p, errors.New("代理服务器地址请勿包含端口，端口请单独填写")
	}
	if p.Port < 1 || p.Port > 65535 {
		return p, errors.New("代理端口需为 1–65535")
	}
	u := url.URL{Scheme: p.Protocol, Host: net.JoinHostPort(p.Host, strconv.Itoa(p.Port))}
	if _, err := url.Parse(u.String()); err != nil {
		return p, errors.New("代理服务器地址无效")
	}
	p.Location = strings.TrimSpace(p.Location)
	p.Note = strings.TrimSpace(p.Note)
	p.Username = strings.TrimSpace(p.Username)
	if len(p.Location) > 128 || strings.ContainsAny(p.Location, "\r\n\x00") {
		return p, errors.New("代理地理位置不能超过 128 字节")
	}
	if len(p.Note) > 200 || strings.ContainsAny(p.Note, "\r\n\x00") {
		return p, errors.New("代理备注不能超过 200 字节")
	}
	if len(p.Username) > 255 || strings.ContainsAny(p.Username, "\r\n\x00") {
		return p, errors.New("代理用户名无效")
	}
	return p, nil
}

func scanProxyRow(scan func(dest ...any) error) (ProxyServer, error) {
	var p ProxyServer
	var healthy sql.NullInt64
	if err := scan(&p.ID, &p.Protocol, &p.Host, &p.Port, &p.Location, &p.Note, &p.Username, &p.Password, &healthy, &p.LatencyMS, &p.LastError, &p.CheckedAt, &p.AccountCount, &p.CreatedAt, &p.UpdatedAt); err != nil {
		return ProxyServer{}, err
	}
	p.HasPassword = len(p.Password) > 0
	if healthy.Valid {
		v := healthy.Int64 != 0
		p.Healthy = &v
	}
	return p, nil
}

func (s *Store) Proxies(ctx context.Context) ([]ProxyServer, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT p.id,p.protocol,p.host,p.port,p.location,p.note,p.username,p.password,p.healthy,p.latency_ms,p.last_error,p.checked_at,(SELECT count(*) FROM upstream_accounts a WHERE a.proxy_id=p.id AND a.deleted_at IS NULL),p.created_at,p.updated_at FROM proxy_servers p ORDER BY p.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ProxyServer{}
	for rows.Next() {
		p, err := scanProxyRow(rows.Scan)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (s *Store) ContributionProxies(ctx context.Context) ([]ContributionProxy, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT id,location,note,healthy FROM proxy_servers ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ContributionProxy{}
	for rows.Next() {
		var p ContributionProxy
		var healthy sql.NullInt64
		if err = rows.Scan(&p.ID, &p.Location, &p.Note, &healthy); err != nil {
			return nil, err
		}
		if healthy.Valid {
			v := healthy.Int64 != 0
			p.Healthy = &v
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (s *Store) Proxy(ctx context.Context, id int64) (ProxyServer, error) {
	row := s.DB.QueryRowContext(ctx, `SELECT p.id,p.protocol,p.host,p.port,p.location,p.note,p.username,p.password,p.healthy,p.latency_ms,p.last_error,p.checked_at,(SELECT count(*) FROM upstream_accounts a WHERE a.proxy_id=p.id AND a.deleted_at IS NULL),p.created_at,p.updated_at FROM proxy_servers p WHERE p.id=?`, id)
	p, err := scanProxyRow(row.Scan)
	if errors.Is(err, sql.ErrNoRows) {
		return ProxyServer{}, ErrNotFound
	}
	return p, err
}

func (s *Store) CreateProxy(ctx context.Context, actor int64, p ProxyServer, seal func(int64) ([]byte, error)) (int64, error) {
	var err error
	p, err = NormalizeProxyServer(p)
	if err != nil {
		return 0, err
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	now := time.Now().Unix()
	res, err := tx.ExecContext(ctx, `INSERT INTO proxy_servers(protocol,host,port,location,note,username,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?)`, p.Protocol, p.Host, p.Port, p.Location, p.Note, p.Username, now, now)
	if err != nil {
		return 0, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, err
	}
	if seal != nil {
		ciphertext, err := seal(id)
		if err != nil {
			return 0, err
		}
		if len(ciphertext) > 0 {
			if _, err = tx.ExecContext(ctx, `UPDATE proxy_servers SET password=? WHERE id=?`, ciphertext, id); err != nil {
				return 0, err
			}
		}
	}
	if actor > 0 {
		if err = audit(ctx, tx, actor, "create_proxy_server", id); err != nil {
			return 0, err
		}
	}
	return id, tx.Commit()
}

func (s *Store) UpdateProxy(ctx context.Context, actor, id int64, p ProxyServer, updatePassword bool, seal func(int64) ([]byte, error)) error {
	var err error
	p, err = NormalizeProxyServer(p)
	if err != nil {
		return err
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	now := time.Now().Unix()
	res, err := tx.ExecContext(ctx, `UPDATE proxy_servers SET protocol=?,host=?,port=?,location=?,note=?,username=?,healthy=NULL,latency_ms=NULL,last_error='',checked_at=NULL,updated_at=? WHERE id=?`, p.Protocol, p.Host, p.Port, p.Location, p.Note, p.Username, now, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	if updatePassword {
		var ciphertext []byte
		if seal != nil {
			ciphertext, err = seal(id)
			if err != nil {
				return err
			}
		}
		if _, err = tx.ExecContext(ctx, `UPDATE proxy_servers SET password=? WHERE id=?`, ciphertext, id); err != nil {
			return err
		}
	}
	if actor > 0 {
		if err = audit(ctx, tx, actor, "update_proxy_server", id); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) DeleteProxy(ctx context.Context, actor, id int64) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var count int
	if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM upstream_accounts WHERE proxy_id=? AND deleted_at IS NULL`, id).Scan(&count); err != nil {
		return err
	}
	if count > 0 {
		return ErrProxyInUse
	}
	if _, err = tx.ExecContext(ctx, `UPDATE upstream_accounts SET proxy_id=NULL WHERE proxy_id=? AND deleted_at IS NOT NULL`, id); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE account_contribution_links SET proxy_id=NULL WHERE proxy_id=?`, id); err != nil {
		return err
	}
	res, err := tx.ExecContext(ctx, `DELETE FROM proxy_servers WHERE id=?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	if actor > 0 {
		if err = audit(ctx, tx, actor, "delete_proxy_server", id); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) SaveProxyHealth(ctx context.Context, id int64, healthy bool, latencyMS *int, lastError string) error {
	h := 0
	if healthy {
		h = 1
		lastError = ""
	}
	res, err := s.DB.ExecContext(ctx, `UPDATE proxy_servers SET healthy=?,latency_ms=?,last_error=?,checked_at=? WHERE id=?`, h, latencyMS, lastError, time.Now().Unix(), id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}
