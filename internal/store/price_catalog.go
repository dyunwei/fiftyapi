package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"
)

type Maintenance struct {
	LastSuccess int64  `json:"last_success"`
	LastAttempt int64  `json:"last_attempt"`
	Error       string `json:"error"`
	Revision    string `json:"revision"`
}

func (s *Store) MaintenanceState(ctx context.Context, name string) (Maintenance, error) {
	var m Maintenance
	err := s.DB.QueryRowContext(ctx, "SELECT last_success,last_attempt,error,revision FROM maintenance_state WHERE name=?", name).Scan(&m.LastSuccess, &m.LastAttempt, &m.Error, &m.Revision)
	if errors.Is(err, sql.ErrNoRows) {
		err = nil
	}
	return m, err
}
func (s *Store) RecordMaintenance(ctx context.Context, name, note string) error {
	_, err := s.DB.ExecContext(ctx, `INSERT INTO maintenance_state(name,last_attempt,error) VALUES(?,?,?) ON CONFLICT(name) DO UPDATE SET last_attempt=excluded.last_attempt,error=excluded.error`, name, time.Now().Unix(), note)
	return err
}

func (s *Store) SavePriceCatalog(ctx context.Context, prices map[string]Price, revision string) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	now := time.Now().Unix()
	for key, p := range prices {
		p.Revision = revision
		p.Source = "litellm"
		raw, e := json.Marshal(p)
		if e != nil {
			return e
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO price_catalog(key,snapshot,revision,synced_at) VALUES(?,?,?,?) ON CONFLICT(key) DO UPDATE SET snapshot=excluded.snapshot,revision=excluded.revision,synced_at=excluded.synced_at`, key, string(raw), revision, now); err != nil {
			return err
		}
	}
	// Missing entries remain last-known prices, rather than silently unpricing a
	// deployed model. Their original revision remains visible for diagnosis.
	if _, err = tx.ExecContext(ctx, `INSERT INTO maintenance_state(name,last_success,last_attempt,error,revision) VALUES('prices',?,?, '',?) ON CONFLICT(name) DO UPDATE SET last_success=excluded.last_success,last_attempt=excluded.last_attempt,error='',revision=excluded.revision`, now, now, revision); err != nil {
		return err
	}
	if err = applyCatalogTx(ctx, tx); err != nil {
		return err
	}
	return tx.Commit()
}
func (s *Store) ApplyPriceCatalog(ctx context.Context) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = applyCatalogTx(ctx, tx); err != nil {
		return err
	}
	return tx.Commit()
}
func applyCatalogTx(ctx context.Context, tx *sql.Tx) error {
	rows, err := tx.QueryContext(ctx, `SELECT m.id,m.provider,m.upstream_id,m.pricing_key,COALESCE(p.source,''),COALESCE(p.rules,'') FROM models m LEFT JOIN model_prices p ON p.id=(SELECT id FROM model_prices WHERE model_id=m.id ORDER BY id DESC LIMIT 1)`)
	if err != nil {
		return err
	}
	type model struct{ id, provider, upstream, key, source, rules string }
	models := []model{}
	for rows.Next() {
		var m model
		if err = rows.Scan(&m.id, &m.provider, &m.upstream, &m.key, &m.source, &m.rules); err != nil {
			rows.Close()
			return err
		}
		models = append(models, m)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, m := range models {
		if m.source == "manual" {
			continue
		}
		key := m.key
		if key == "" {
			switch m.provider {
			case "openai":
				key = "openai/" + m.upstream
			case "claude":
				key = "anthropic/" + m.upstream
			default:
				key = m.upstream
			}
		}
		var raw string
		err = tx.QueryRowContext(ctx, "SELECT snapshot FROM price_catalog WHERE key=?", key).Scan(&raw)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return err
		}
		var p Price
		if json.Unmarshal([]byte(raw), &p) != nil {
			return errors.New("本地价格目录无效")
		}
		// The source key is explicit for gateways; bare catalog IDs are accepted
		// only when that exact ID exists in the upstream catalog, never fuzzy matched.
		encoded, _ := json.Marshal(p)
		if string(encoded) == m.rules {
			continue
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO model_prices(model_id,input,output,cached,cache_write,created_at,source,rules) VALUES(?,?,?,?,?,?,'litellm',?)`, m.id, p.Input, p.Output, p.Cached, p.CacheWrite, time.Now().Unix(), string(encoded)); err != nil {
			return err
		}
	}
	return nil
}
func (s *Store) SetPricingKey(ctx context.Context, actor int64, model, key string) error {
	if len(key) > 512 {
		return errors.New("价格目录键过长")
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	r, err := tx.ExecContext(ctx, "UPDATE models SET pricing_key=? WHERE id=?", key, model)
	if err != nil {
		return err
	}
	n, _ := r.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	// Returning to catalog pricing is an explicit action; immutable historical
	// manual versions stay in place, followed by a new catalog version.
	var raw string
	if key != "" {
		if err = tx.QueryRowContext(ctx, "SELECT snapshot FROM price_catalog WHERE key=?", key).Scan(&raw); err != nil {
			return errors.New("价格目录中找不到该模型")
		}
		var p Price
		if err = json.Unmarshal([]byte(raw), &p); err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO model_prices(model_id,input,output,cached,cache_write,created_at,source,rules) VALUES(?,?,?,?,?,?,'litellm',?)`, model, p.Input, p.Output, p.Cached, p.CacheWrite, time.Now().Unix(), raw); err != nil {
			return err
		}
	}
	details, _ := json.Marshal(map[string]string{"model": model, "pricing_key": key})
	if _, err = tx.ExecContext(ctx, "INSERT INTO admin_audit_logs(actor_id,action,details,created_at) VALUES(?,'pricing_key',?,?)", actor, string(details), time.Now().Unix()); err != nil {
		return err
	}
	return tx.Commit()
}
