package store

import (
	"context"
	"database/sql"
	"encoding/json"

	"strings"
)

type ModelPage struct {
	Items    []Model `json:"items"`
	Total    int     `json:"total"`
	Page     int     `json:"page"`
	PageSize int     `json:"page_size"`
}

type ModelFilter struct {
	UserID        int64
	AllowedModels *[]string
	AccountID     int64
	Protocol      string
	Status        string
}

func (s *Store) ModelPage(ctx context.Context, page, size int, search string, public bool, filters ...ModelFilter) (ModelPage, error) {
	out := ModelPage{Items: []Model{}, Page: page, PageSize: size}
	if page < 1 || page > 1000000 || size < 1 || size > 100 {
		return out, ErrNotFound
	}
	prefix, protocols, available := "", "m.protocols", catalogAvailability
	from := ` FROM models m LEFT JOIN model_prices p ON p.id=(SELECT id FROM model_prices WHERE model_id=m.id ORDER BY id DESC LIMIT 1)`
	where := ` WHERE 1=1`
	args := []any{}
	if len(filters) > 0 && filters[0].UserID > 0 {
		prefix, protocols, available = userCatalog, userCatalogProtocols, userCatalogAvailability
		where += " AND " + userCatalogVisibility
		args = append(args, filters[0].UserID)
	}
	if q := strings.TrimSpace(search); q != "" {
		where += ` AND (instr(lower(m.id),lower(?))>0 OR instr(lower(m.provider),lower(?))>0 OR instr(lower(m.upstream_id),lower(?))>0)`
		args = append(args, q, q, q)
	}
	if len(filters) > 0 {
		f := filters[0]
		if f.AllowedModels != nil {
			raw, _ := json.Marshal(*f.AllowedModels)
			where += ` AND m.id IN (SELECT value FROM json_each(?))`
			args = append(args, string(raw))
		}
		if f.AccountID > 0 {
			where += ` AND EXISTS(SELECT 1 FROM account_models fam JOIN upstream_accounts fa ON fa.id=fam.account_id WHERE fam.model_id=m.id AND fam.account_id=? AND fa.deleted_at IS NULL)`
			args = append(args, f.AccountID)
		}
		if f.Protocol != "" {
			where += ` AND EXISTS(SELECT 1 FROM json_each(` + protocols + `) WHERE value=?)`
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
	if err = tx.QueryRowContext(ctx, prefix+`SELECT count(*)`+from+where, args...).Scan(&out.Total); err != nil {
		return out, err
	}
	rows, err := tx.QueryContext(ctx, prefix+`SELECT m.id,m.provider,m.upstream_id,m.enabled,m.synced_at,`+protocols+`,m.pricing_key,`+available+`,p.id,p.input,p.output,p.cached,p.cache_write,p.source,p.rules`+from+where+` ORDER BY m.provider,m.id LIMIT ? OFFSET ?`, append(args, size, (page-1)*size)...)
	if err != nil {
		return out, err
	}
	for rows.Next() {
		var m Model
		var protocols string
		var priceID, input, output, cached, write sql.NullInt64
		var source, rules sql.NullString
		if err = rows.Scan(&m.ID, &m.Provider, &m.UpstreamID, &m.Enabled, &m.SyncedAt, &protocols, &m.PricingKey, &m.Available, &priceID, &input, &output, &cached, &write, &source, &rules); err != nil {
			rows.Close()
			return out, err
		}
		m.setProtocols(protocols)
		if priceID.Valid {
			var extra Price
			_ = json.Unmarshal([]byte(rules.String), &extra)
			m.Price = &Price{MaxOutput: extra.MaxOutput, Source: source.String, Revision: extra.Revision, Rates: extra.Rates, ID: priceID.Int64, Input: input.Int64, Output: output.Int64, Cached: cached.Int64, CacheWrite: write.Int64}
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
