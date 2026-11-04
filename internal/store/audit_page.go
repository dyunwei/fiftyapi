package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
)

type AuditFilter struct {
	ActorID, TargetID, Since, Until int64
	Action, Details                 string
}
type AuditPage struct {
	Items    []map[string]any `json:"items"`
	Total    int              `json:"total"`
	Page     int              `json:"page"`
	PageSize int              `json:"page_size"`
}

func (s *Store) AuditListPage(ctx context.Context, f AuditFilter, page, size int) (AuditPage, error) {
	out := AuditPage{Items: []map[string]any{}, Page: page, PageSize: size}
	if page < 1 || page > 1000000 || size < 1 || size > 100 {
		return out, ErrNotFound
	}
	where := " FROM admin_audit_logs WHERE 1=1"
	args := []any{}
	for _, v := range []struct {
		column string
		value  int64
	}{{"actor_id", f.ActorID}, {"target_id", f.TargetID}} {
		if v.value > 0 {
			where += " AND " + v.column + "=?"
			args = append(args, v.value)
		}
	}
	for _, v := range []struct{ column, value string }{{"action", f.Action}, {"details", f.Details}} {
		if q := strings.TrimSpace(v.value); q != "" {
			where += " AND instr(lower(" + v.column + "),lower(?))>0"
			args = append(args, q)
		}
	}
	if f.Since > 0 {
		where += " AND created_at>=?"
		args = append(args, f.Since)
	}
	if f.Until > 0 {
		where += " AND created_at<=?"
		args = append(args, f.Until)
	}
	tx, err := s.DB.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return out, err
	}
	defer tx.Rollback()
	if err = tx.QueryRowContext(ctx, "SELECT count(*)"+where, args...).Scan(&out.Total); err != nil {
		return out, err
	}
	rows, err := tx.QueryContext(ctx, "SELECT id,actor_id,action,target_id,details,created_at"+where+" ORDER BY id DESC LIMIT ? OFFSET ?", append(args, size, (page-1)*size)...)
	if err != nil {
		return out, err
	}
	for rows.Next() {
		var id, created int64
		var actor, target *int64
		var action, details string
		if err = rows.Scan(&id, &actor, &action, &target, &details, &created); err != nil {
			rows.Close()
			return out, err
		}
		var parsed any
		if json.Unmarshal([]byte(details), &parsed) != nil {
			parsed = details
		}
		out.Items = append(out.Items, map[string]any{"id": id, "actor_id": actor, "action": action, "target_id": target, "details": parsed, "created_at": created})
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return out, err
	}
	return out, tx.Commit()
}
