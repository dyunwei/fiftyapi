package store

import (
	"context"
	"database/sql"
)

type LogFilter struct {
	User                         int64
	Admin                        bool
	Model, RequestID, Settlement string
	Status                       int
	Since, Until                 int64
}
type LogPage struct {
	Items    []RequestLog `json:"items"`
	Total    int          `json:"total"`
	Page     int          `json:"page"`
	PageSize int          `json:"page_size"`
}

func logPageSources(f LogFilter) (string, string, []any) {
	normal := " FROM request_logs l"
	if f.Settlement != "" {
		normal += " JOIN request_charges c ON c.id=l.id"
	}
	normal += " WHERE 1=1"
	missing := ` FROM request_charges c WHERE c.status='pending' AND NOT EXISTS(SELECT 1 FROM request_logs x WHERE x.id=c.id)`
	filters := func(alias, model string, synthetic bool) (string, []any) {
		where := ""
		args := []any{}
		if !f.Admin {
			where += " AND " + alias + ".user_id=?"
			args = append(args, f.User)
		}
		if f.Model != "" {
			where += " AND " + alias + "." + model + "=?"
			args = append(args, f.Model)
		}
		if f.RequestID != "" {
			where += " AND " + alias + ".id=?"
			args = append(args, f.RequestID)
		}
		if f.Status > 0 {
			if synthetic {
				where += " AND 0=?"
			} else {
				where += " AND " + alias + ".status=?"
			}
			args = append(args, f.Status)
		}
		if f.Since > 0 {
			where += " AND " + alias + ".created_at>=?"
			args = append(args, f.Since)
		}
		if f.Until > 0 {
			where += " AND " + alias + ".created_at<=?"
			args = append(args, f.Until)
		}

		if f.Settlement != "" {
			where += " AND c.status=?"
			args = append(args, f.Settlement)
		}
		return where, args
	}
	nw, na := filters("l", "model", false)
	mw, ma := filters("c", "model_id", true)
	return normal + nw, missing + mw, append(na, ma...)
}

func (s *Store) LogListPage(ctx context.Context, f LogFilter, page, size int) (LogPage, error) {
	out := LogPage{Items: []RequestLog{}, Page: page, PageSize: size}
	if page < 1 || page > 1000000 || size < 1 || size > 100 {
		return out, ErrNotFound
	}
	normal, missing, args := logPageSources(f)
	// Count using each source's filters and indexes, without materializing full
	// log rows. The second branch retains ledger exceptions with missing logs.
	tx, err := s.DB.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return out, err
	}
	defer tx.Rollback()
	if err = tx.QueryRowContext(ctx, "SELECT (SELECT count(*)"+normal+")+(SELECT count(*)"+missing+")", args...).Scan(&out.Total); err != nil {
		return out, err
	}
	// Limit the ID streams before joining wide metadata. SQLite can merge the
	// ordered sources using time indexes; only this page needs full-row reads.
	query := `WITH page_ids AS (SELECT l.id,l.created_at` + normal + ` UNION ALL SELECT c.id,c.created_at` + missing + ` ORDER BY created_at DESC,id DESC LIMIT ? OFFSET ?)
 SELECT p.id,COALESCE(l.user_id,c.user_id),COALESCE(l.key_id,c.key_id,0),l.account_id,COALESCE(l.model,c.model_id),COALESCE(l.provider,m.provider,''),COALESCE(l.status,0),l.input_tokens,l.output_tokens,l.cached_tokens,l.cache_write_tokens,l.amount,COALESCE(l.duration_ms,0),CASE WHEN l.id IS NULL THEN '结算日志未完整保存，请核查账本记录' ELSE l.error END,p.created_at,COALESCE(c.status,''),CASE WHEN c.status='pending' THEN c.reservation ELSE NULL END
 FROM page_ids p LEFT JOIN request_logs l ON l.id=p.id LEFT JOIN request_charges c ON c.id=p.id LEFT JOIN models m ON m.id=c.model_id ORDER BY p.created_at DESC,p.id DESC`
	rows, err := tx.QueryContext(ctx, query, append(args, size, (page-1)*size)...)
	if err != nil {
		return out, err
	}
	for rows.Next() {
		var l RequestLog
		if err = rows.Scan(&l.ID, &l.UserID, &l.KeyID, &l.AccountID, &l.Model, &l.Provider, &l.Status, &l.Input, &l.Output, &l.Cached, &l.CacheWrite, &l.Amount, &l.DurationMS, &l.Error, &l.CreatedAt, &l.SettlementStatus, &l.Reservation); err != nil {
			rows.Close()
			return out, err
		}
		out.Items = append(out.Items, l)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return out, err
	}
	return out, tx.Commit()
}

func (s *Store) Charge(ctx context.Context, id string) (Charge, error) {
	var c Charge
	err := s.DB.QueryRowContext(ctx, `SELECT c.id,c.user_id,c.key_id,c.model_id,c.reservation,c.amount,c.status,c.created_at,COALESCE(NULLIF(l.error,''),'结算日志未完整保存') FROM request_charges c LEFT JOIN request_logs l ON l.id=c.id WHERE c.id=?`, id).Scan(&c.ID, &c.UserID, &c.KeyID, &c.ModelID, &c.Reservation, &c.Amount, &c.Status, &c.CreatedAt, &c.Error)
	return c, err
}
