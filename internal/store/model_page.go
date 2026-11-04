package store

import (
	"context"
	"database/sql"

	"strings"
)

type ModelPage struct {
	Items    []Model `json:"items"`
	Total    int     `json:"total"`
	Page     int     `json:"page"`
	PageSize int     `json:"page_size"`
}

type ModelFilter struct {
	AccountID int64
	Protocol  string
	Status    string
}

func (s *Store) ModelPage(ctx context.Context, page, size int, search string, public bool, filters ...ModelFilter) (ModelPage, error) {
	out := ModelPage{Items: []Model{}, Page: page, PageSize: size}
	if page < 1 || page > 1000000 || size < 1 || size > 100 {
		return out, ErrNotFound
	}
	available := `EXISTS(SELECT 1 FROM account_models am JOIN upstream_accounts a ON a.id=am.account_id WHERE am.model_id=m.id AND a.enabled=1 AND a.state IN ('catalog_ready','ready'))`
	from := ` FROM models m LEFT JOIN model_prices p ON p.id=(SELECT id FROM model_prices WHERE model_id=m.id ORDER BY id DESC LIMIT 1)`
	where := ` WHERE 1=1`
	args := []any{}
	if q := strings.TrimSpace(search); q != "" {
		where += ` AND (instr(lower(m.id),lower(?))>0 OR instr(lower(m.provider),lower(?))>0 OR instr(lower(m.upstream_id),lower(?))>0)`
		args = append(args, q, q, q)
	}
	if len(filters) > 0 {
		f := filters[0]
		if f.AccountID > 0 {
			where += ` AND EXISTS(SELECT 1 FROM account_models fam JOIN upstream_accounts fa ON fa.id=fam.account_id WHERE fam.model_id=m.id AND fam.account_id=? AND fa.deleted_at IS NULL)`
			args = append(args, f.AccountID)
		}
		if f.Protocol != "" {
			where += ` AND EXISTS(SELECT 1 FROM json_each(m.protocols) WHERE value=?)`
			args = append(args, f.Protocol)
		}
		switch f.Status {
		case "disabled":
			where += ` AND m.enabled=0`
		case "unavailable":
			where += ` AND m.enabled=1 AND NOT ` + available
		case "unpriced":
			where += ` AND m.enabled=1 AND ` + available + ` AND p.id IS NULL`
		case "priced":
			where += ` AND m.enabled=1 AND ` + available + ` AND p.id IS NOT NULL`
		}

	}
	if public {
		where += ` AND m.enabled=1 AND p.id IS NOT NULL AND ` + available
	}
	// A read transaction keeps the count and page consistent during model sync.
	tx, err := s.DB.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return out, err
	}
	defer tx.Rollback()
	if err = tx.QueryRowContext(ctx, `SELECT count(*)`+from+where, args...).Scan(&out.Total); err != nil {
		return out, err
	}
	rows, err := tx.QueryContext(ctx, `SELECT m.id,m.provider,m.upstream_id,m.enabled,m.synced_at,m.protocols,`+available+`,p.id,p.input,p.output,p.cached,p.cache_write`+from+where+` ORDER BY m.provider,m.id LIMIT ? OFFSET ?`, append(args, size, (page-1)*size)...)
	if err != nil {
		return out, err
	}
	for rows.Next() {
		var m Model
		var protocols string
		var priceID, input, output, cached, write sql.NullInt64
		if err = rows.Scan(&m.ID, &m.Provider, &m.UpstreamID, &m.Enabled, &m.SyncedAt, &protocols, &m.Available, &priceID, &input, &output, &cached, &write); err != nil {
			rows.Close()
			return out, err
		}
		m.setProtocols(protocols)
		if priceID.Valid {
			m.Price = &Price{ID: priceID.Int64, Input: input.Int64, Output: output.Int64, Cached: cached.Int64, CacheWrite: write.Int64}
		}
		out.Items = append(out.Items, m)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return out, err
	}
	return out, tx.Commit()
}
