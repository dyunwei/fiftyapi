package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fiftyapi/internal/modelref"
	"math/big"
	"time"
)

type Price struct {
	ID         int64 `json:"id"`
	Input      int64 `json:"input"`
	Output     int64 `json:"output"`
	Cached     int64 `json:"cached"`
	CacheWrite int64 `json:"cache_write"`
}
type Model struct {
	Available  bool     `json:"available"`
	Protocol   string   `json:"protocol"`
	Protocols  []string `json:"protocols"`
	ID         string   `json:"id"`
	Provider   string   `json:"provider"`
	UpstreamID string   `json:"upstream_id"`
	Enabled    bool     `json:"enabled"`
	SyncedAt   int64    `json:"synced_at"`
	Price      *Price   `json:"price"`
}
type TokenUsage struct {
	Input      int64 `json:"input"`
	Output     int64 `json:"output"`
	Cached     int64 `json:"cached"`
	CacheWrite int64 `json:"cache_write"`
}

// Prices are microUSD per million tokens. Round once at request settlement.
func (p Price) Cost(u TokenUsage) (int64, error) {
	sum := new(big.Int)
	for _, pair := range [][2]int64{{p.Input, u.Input}, {p.Output, u.Output}, {p.Cached, u.Cached}, {p.CacheWrite, u.CacheWrite}} {
		if pair[0] < 0 || pair[1] < 0 {
			return 0, errors.New("价格与用量不能为负数")
		}
		sum.Add(sum, new(big.Int).Mul(big.NewInt(pair[0]), big.NewInt(pair[1])))
	}
	sum.Add(sum, big.NewInt(999999))
	sum.Div(sum, big.NewInt(1000000))
	if !sum.IsInt64() {
		return 0, errors.New("计费金额溢出")
	}
	return sum.Int64(), nil
}
func (s *Store) SyncModels(ctx context.Context, account int64, provider string, ids []string) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var exists int
	if err = tx.QueryRowContext(ctx, "SELECT 1 FROM upstream_accounts WHERE id=? AND deleted_at IS NULL", account).Scan(&exists); err != nil {
		return ErrNotFound
	}
	if _, err = tx.ExecContext(ctx, "DELETE FROM account_models WHERE account_id=?", account); err != nil {
		return err
	}
	for _, upstream := range ids {
		var id string
		err = tx.QueryRowContext(ctx, "SELECT id FROM models WHERE provider=? AND upstream_id=?", provider, upstream).Scan(&id)
		if errors.Is(err, sql.ErrNoRows) {
			id = upstream
			var n int
			tx.QueryRowContext(ctx, "SELECT count(*) FROM models WHERE id=?", id).Scan(&n)
			if n > 0 {
				id = provider + "/" + upstream
			}
			if _, err = tx.ExecContext(ctx, "INSERT INTO models(id,provider,upstream_id,synced_at) VALUES(?,?,?,?)", id, provider, upstream, time.Now().Unix()); err != nil {
				return err
			}
		} else if err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, "UPDATE models SET synced_at=?,protocols=? WHERE id=?", time.Now().Unix(), `["`+modelref.Protocol(provider)+`"]`, id); err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, "INSERT OR IGNORE INTO account_models(account_id,model_id) VALUES(?,?)", account, id); err != nil {
			return err
		}
		if p, ok := builtinPrice(upstream); ok {
			if _, err = tx.ExecContext(ctx, "INSERT INTO model_prices(model_id,input,output,cached,cache_write,created_at) SELECT ?,?,?,?,?,? WHERE NOT EXISTS(SELECT 1 FROM model_prices WHERE model_id=?)", id, p.Input, p.Output, p.Cached, p.CacheWrite, time.Now().Unix(), id); err != nil {
				return err
			}
		}
	}
	if _, err = tx.ExecContext(ctx, "UPDATE upstream_accounts SET models_synced_at=? WHERE id=?", time.Now().Unix(), account); err != nil {
		return err
	}
	return tx.Commit()
}
func (s *Store) Models(ctx context.Context) ([]Model, error) {
	rows, err := s.DB.QueryContext(ctx, "SELECT m.id,m.provider,m.upstream_id,m.enabled,m.synced_at,m.protocols,EXISTS(SELECT 1 FROM account_models am JOIN upstream_accounts a ON a.id=am.account_id WHERE am.model_id=m.id AND a.enabled=1 AND a.state IN ('catalog_ready','ready')) FROM models m ORDER BY m.provider,m.id")
	if err != nil {
		return nil, err
	}
	models := []Model{}
	for rows.Next() {
		var m Model
		var protocols string
		if err = rows.Scan(&m.ID, &m.Provider, &m.UpstreamID, &m.Enabled, &m.SyncedAt, &protocols, &m.Available); err != nil {
			rows.Close()
			return nil, err
		}
		m.setProtocols(protocols)
		models = append(models, m)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	for i := range models {
		p, err := s.Price(ctx, models[i].ID)
		if err == nil {
			models[i].Price = &p
		} else if !errors.Is(err, sql.ErrNoRows) {
			return nil, err
		}
	}
	return models, nil
}
func (s *Store) Price(ctx context.Context, id string) (Price, error) {
	var p Price
	err := s.DB.QueryRowContext(ctx, "SELECT id,input,output,cached,cache_write FROM model_prices WHERE model_id=? ORDER BY id DESC LIMIT 1", id).Scan(&p.ID, &p.Input, &p.Output, &p.Cached, &p.CacheWrite)
	return p, err
}
func (s *Store) SetModel(ctx context.Context, actor int64, id string, enabled bool, price *Price) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	r, err := tx.ExecContext(ctx, "UPDATE models SET enabled=? WHERE id=?", enabled, id)
	if err != nil {
		return err
	}
	n, _ := r.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	if price != nil {
		for _, n := range []int64{price.Input, price.Output, price.Cached, price.CacheWrite} {
			if n < 0 || n > 1000000000000 {
				return errors.New("价格超出范围")
			}
		}
		if _, err = tx.ExecContext(ctx, "INSERT INTO model_prices(model_id,input,output,cached,cache_write,created_at) VALUES(?,?,?,?,?,?)", id, price.Input, price.Output, price.Cached, price.CacheWrite, time.Now().Unix()); err != nil {
			return err
		}
	}
	details, _ := json.Marshal(map[string]any{"model": id, "enabled": enabled, "price": price})
	if _, err = tx.ExecContext(ctx, "INSERT INTO admin_audit_logs(actor_id,action,details,created_at) VALUES(?,'set_model',?,?)", actor, string(details), time.Now().Unix()); err != nil {
		return err
	}
	return tx.Commit()
}
func (s *Store) RoutableModel(ctx context.Context, id string) (Model, []int64, error) {
	var m Model
	var protocols string
	err := s.DB.QueryRowContext(ctx, "SELECT id,provider,upstream_id,enabled,synced_at,protocols FROM models WHERE id=? AND enabled=1", id).Scan(&m.ID, &m.Provider, &m.UpstreamID, &m.Enabled, &m.SyncedAt, &protocols)
	if err != nil {
		return m, nil, err
	}
	p, err := s.Price(ctx, id)
	if err != nil {
		return m, nil, errors.New("模型尚未配置价格")
	}
	m.Price = &p
	m.setProtocols(protocols)
	rows, err := s.DB.QueryContext(ctx, `SELECT a.id FROM account_models am JOIN upstream_accounts a ON a.id=am.account_id WHERE am.model_id=? AND a.enabled=1 AND a.state IN ('catalog_ready','ready') ORDER BY a.id`, id)
	if err != nil {
		return m, nil, err
	}
	defer rows.Close()
	ids := []int64{}
	for rows.Next() {
		var id int64
		if err = rows.Scan(&id); err != nil {
			return m, nil, err
		}
		ids = append(ids, id)
	}
	if len(ids) == 0 {
		return m, nil, errors.New("没有可用上游账号")
	}
	m.Available = true
	return m, ids, rows.Err()
}

func (m *Model) setProtocols(raw string) {
	_ = json.Unmarshal([]byte(raw), &m.Protocols)
	if len(m.Protocols) == 0 {
		if p := modelref.Protocol(m.Provider); p != "" {
			m.Protocols = []string{p}
		}
	}
	if len(m.Protocols) > 0 {
		m.Protocol = m.Protocols[0]
	}
}
func (m Model) SupportsProtocol(protocol string) bool {
	for _, p := range m.Protocols {
		if p == protocol {
			return true
		}
	}
	return len(m.Protocols) == 0 && (m.Protocol == protocol || modelref.Protocol(m.Provider) == protocol)
}
