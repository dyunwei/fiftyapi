package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fiftyapi/internal/modelref"
	"fmt"
	"time"
)

func (s *Store) RelayInstanceID(ctx context.Context) (string, error) {
	var id string
	err := s.DB.QueryRowContext(ctx, "SELECT value FROM settings WHERE key='relay_instance_id'").Scan(&id)
	return id, err
}

// SyncGatewayModels attaches gateway routes to public model pools. A mapping's
// ID is the public pool name and UpstreamID is private to this account. When ID
// is omitted, the upstream name is intentionally used, so equal names from
// OAuth and API gateways join the same pool.
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

	type previousRoute struct{ model, upstream string }
	previous := []previousRoute{}
	rows, err := tx.QueryContext(ctx, "SELECT model_id,upstream_id FROM account_models WHERE account_id=?", account)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var route previousRoute
		if err = rows.Scan(&route.model, &route.upstream); err != nil {
			rows.Close()
			return nil, err
		}
		previous = append(previous, route)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	if _, err = tx.ExecContext(ctx, "DELETE FROM account_models WHERE account_id=?", account); err != nil {
		return nil, err
	}

	affected := map[string]bool{}
	for _, route := range previous {
		affected[route.model] = true
	}
	seen := map[string]bool{}
	result := make([]string, 0, len(mappings))
	for _, mapping := range mappings {
		protocols := mapping.SupportedProtocols()
		if len(protocols) == 0 {
			return nil, errors.New("模型缺少协议")
		}
		for _, protocol := range protocols {
			if !modelref.ValidProtocol(protocol) {
				return nil, errors.New("模型协议无效")
			}
		}
		id := mapping.ID
		if id == "" {
			id = mapping.UpstreamID
		}
		if id == "" || mapping.UpstreamID == "" || seen[id] {
			return nil, errors.New("同一账号的模型路由 ID 必须唯一")
		}
		seen[id] = true
		primary := mapping.Protocol
		if primary == "" {
			primary = protocols[0]
		}
		encoded, _ := json.Marshal(protocols)

		var current string
		err = tx.QueryRowContext(ctx, "SELECT id FROM models WHERE id=?", id).Scan(&current)
		if errors.Is(err, sql.ErrNoRows) {
			// models still carries a descriptive provider for catalogs. Make it
			// unique per logical pool; actual dispatch uses the account route.
			provider := modelref.GatewayProvider(account, primary) + "/" + id
			if _, err = tx.ExecContext(ctx, "INSERT INTO models(id,provider,upstream_id,synced_at,protocols) VALUES(?,?,?,?,?)", id, provider, mapping.UpstreamID, time.Now().Unix(), string(encoded)); err != nil {
				return nil, fmt.Errorf("创建模型路由池失败：%w", err)
			}
			// Preserve the latest local price for a sole route rename. Shared
			// pools are never renamed or mutated by an account sync.
			for _, old := range previous {
				if old.upstream != mapping.UpstreamID || old.model == id {
					continue
				}
				var others int
				_ = tx.QueryRowContext(ctx, "SELECT count(*) FROM account_models WHERE model_id=?", old.model).Scan(&others)
				if others == 0 {
					_, _ = tx.ExecContext(ctx, `INSERT INTO model_prices(model_id,input,output,cached,cache_write,created_at)
					 SELECT ?,input,output,cached,cache_write,? FROM model_prices WHERE model_id=? ORDER BY id DESC LIMIT 1`, id, time.Now().Unix(), old.model)
				}
			}
		} else if err != nil {
			return nil, err
		}
		if _, err = tx.ExecContext(ctx, "INSERT INTO account_models(account_id,model_id,upstream_id,protocols) VALUES(?,?,?,?)", account, id, mapping.UpstreamID, string(encoded)); err != nil {
			return nil, err
		}
		affected[id] = true
		result = append(result, id)
	}
	for id := range affected {
		if err = refreshModelProtocols(ctx, tx, id); err != nil {
			return nil, err
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
