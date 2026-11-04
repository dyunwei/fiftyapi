package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fiftyapi/internal/modelref"
	"fmt"
	"strings"
	"time"
)

func (s *Store) RelayInstanceID(ctx context.Context) (string, error) {
	var id string
	err := s.DB.QueryRowContext(ctx, "SELECT value FROM settings WHERE key='relay_instance_id'").Scan(&id)
	return id, err
}
func (s *Store) SyncGatewayModels(ctx context.Context, account int64, mappings []modelref.Mapping) ([]string, error) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	var exists int
	if err = tx.QueryRowContext(ctx, "SELECT 1 FROM upstream_accounts WHERE id=? AND deleted_at IS NULL", account).Scan(&exists); err != nil {
		return nil, ErrNotFound
	}
	if _, err = tx.ExecContext(ctx, "PRAGMA defer_foreign_keys=ON"); err != nil {
		return nil, err
	}
	if _, err = tx.ExecContext(ctx, "DELETE FROM account_models WHERE account_id=?", account); err != nil {
		return nil, err
	}
	result := []string{}
	for _, mapping := range mappings {
		protocols := mapping.SupportedProtocols()
		if len(protocols) == 0 {
			return nil, errors.New("模型缺少协议")
		}
		primary := mapping.Protocol
		if primary == "" {
			primary = protocols[0]
		}
		provider := modelref.GatewayProvider(account, primary)
		encoded, _ := json.Marshal(protocols)
		var existing string
		err = tx.QueryRowContext(ctx, "SELECT id FROM models WHERE provider=? AND upstream_id=?", provider, mapping.UpstreamID).Scan(&existing)

		if mapping.Protocol == "" {
			prefix := fmt.Sprintf("gateway/%d/", account)
			err = sql.ErrNoRows
			if mapping.ID != "" {
				err = tx.QueryRowContext(ctx, "SELECT id,provider FROM models WHERE id=? AND provider LIKE ?", mapping.ID, prefix+"%").Scan(&existing, &provider)
			}
			if errors.Is(err, sql.ErrNoRows) {
				aliases := []string{}
				for _, other := range mappings {
					if other.ID != "" && other.ID != mapping.ID {
						aliases = append(aliases, other.ID)
					}
				}
				reserved, _ := json.Marshal(aliases)
				count := 0
				for _, other := range mappings {
					if other.UpstreamID == mapping.UpstreamID {
						count++
					}
				}
				single := 0
				if count == 1 {
					single = 1
				}
				err = tx.QueryRowContext(ctx, `SELECT id,provider FROM models WHERE provider LIKE ? AND upstream_id=? AND NOT EXISTS(SELECT 1 FROM account_models am WHERE am.account_id=? AND am.model_id=models.id) AND id NOT IN (SELECT value FROM json_each(?)) AND (provider=? OR EXISTS(SELECT 1 FROM json_each(?) WHERE value=substr(models.provider,?)) OR ?=1) ORDER BY provider=? DESC,EXISTS(SELECT 1 FROM model_prices p WHERE p.model_id=models.id) DESC,id LIMIT 1`, prefix+"%", mapping.UpstreamID, account, string(reserved), prefix+"models", string(encoded), len(prefix)+1, single, provider).Scan(&existing, &provider)
			}
		}

		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return nil, err
		}
		id := mapping.ID
		if id == "" {
			id = existing
		}
		if id == "" {
			id = provider + "/" + mapping.UpstreamID
		}
		if existing == "" && mapping.ID != "" {
			var owner string
			e := tx.QueryRowContext(ctx, "SELECT provider FROM models WHERE id=?", id).Scan(&owner)
			if e == nil && (owner == provider || strings.HasPrefix(owner, fmt.Sprintf("gateway/%d/", account))) {
				if _, e = tx.ExecContext(ctx, "UPDATE models SET upstream_id=? WHERE id=?", mapping.UpstreamID, id); e != nil {
					return nil, errors.New("模型映射目标冲突，未修改原映射")
				}
				existing = id
				provider = owner
			} else if e != nil && !errors.Is(e, sql.ErrNoRows) {
				return nil, e
			}
		}
		if existing != "" && id != existing {
			for _, q := range []string{"UPDATE models SET id=? WHERE id=?", "UPDATE account_models SET model_id=? WHERE model_id=?", "UPDATE model_prices SET model_id=? WHERE model_id=?"} {
				if _, err = tx.ExecContext(ctx, q, id, existing); err != nil {
					return nil, errors.New("模型别名已被占用，未修改原映射")
				}
			}
		}
		if existing == "" {
			if _, err = tx.ExecContext(ctx, "INSERT INTO models(id,provider,upstream_id,synced_at,protocols) VALUES(?,?,?,?,?)", id, provider, mapping.UpstreamID, time.Now().Unix(), string(encoded)); err != nil {
				return nil, errors.New("模型别名已被占用，请更换别名")
			}
		} else if _, err = tx.ExecContext(ctx, "UPDATE models SET upstream_id=?,synced_at=?,protocols=? WHERE id=?", mapping.UpstreamID, time.Now().Unix(), string(encoded), id); err != nil {
			return nil, err
		}
		if _, err = tx.ExecContext(ctx, "INSERT INTO account_models(account_id,model_id) VALUES(?,?)", account, id); err != nil {
			return nil, err
		}
		// API gateway prices may differ from public vendor prices. Never seed an
		// assumed vendor rate: an administrator must confirm a local price first.
		result = append(result, id)
		if mapping.Protocol == "" {
			// Previously issued protocol-specific IDs remain usable with their own
			// immutable historical prices, only while that protocol is still enabled.
			rows, e := tx.QueryContext(ctx, "SELECT id,provider FROM models WHERE provider LIKE ? AND upstream_id=? AND id<>?", fmt.Sprintf("gateway/%d/%%", account), mapping.UpstreamID, id)
			if e != nil {
				return nil, e
			}
			aliases := map[string]string{}
			for rows.Next() {
				var a, p string
				if e = rows.Scan(&a, &p); e != nil {
					rows.Close()
					return nil, e
				}
				for _, protocol := range protocols {
					if modelref.Protocol(p) == protocol {
						aliases[a] = protocol
					}
				}
			}
			e = rows.Err()
			rows.Close()
			if e != nil {
				return nil, e
			}
			for alias, protocol := range aliases {
				raw, _ := json.Marshal([]string{protocol})
				if _, e = tx.ExecContext(ctx, "UPDATE models SET protocols=? WHERE id=?", string(raw), alias); e != nil {
					return nil, e
				}
				if _, e = tx.ExecContext(ctx, "INSERT OR IGNORE INTO account_models(account_id,model_id) VALUES(?,?)", account, alias); e != nil {
					return nil, e
				}
				result = append(result, alias)
			}
		}
	}
	if _, err = tx.ExecContext(ctx, "UPDATE upstream_accounts SET models_synced_at=? WHERE id=?", time.Now().Unix(), account); err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, fmt.Errorf("保存网关模型失败：%w", err)
	}
	return result, nil
}
