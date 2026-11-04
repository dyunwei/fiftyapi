package store

import (
	"context"
	"errors"
	"log"
	"strings"
	"time"
)

type RuntimeLog struct {
	ID          int64  `json:"id"`
	Level       string `json:"level"`
	Module      string `json:"module"`
	Event       string `json:"event"`
	AccountID   int64  `json:"account_id"`
	Message     string `json:"message"`
	Status      int    `json:"status"`
	DurationMS  int64  `json:"duration_ms"`
	Occurrences int64  `json:"occurrences"`
	CreatedAt   int64  `json:"created_at"`
	LastAt      int64  `json:"last_at"`
}
type RuntimeLogFilter struct {
	Level, Module           string
	AccountID, Since, Until int64
}
type RuntimeLogPage struct {
	Items    []RuntimeLog `json:"items"`
	Total    int          `json:"total"`
	Page     int          `json:"page"`
	PageSize int          `json:"page_size"`
}

// RecordRuntime accepts only caller-authored summaries, never upstream errors,
// headers or bodies. Identical events coalesce into five-minute buckets.
// Logging failure must not change the result of the operation being observed.
func (s *Store) RecordRuntime(ctx context.Context, entry RuntimeLog) {
	task, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
	defer cancel()
	now := time.Now().Unix()
	if entry.Level != "info" && entry.Level != "warn" && entry.Level != "error" {
		entry.Level = "info"
	}
	if len(entry.Message) > 1000 || len(entry.Module) > 64 || len(entry.Event) > 100 {
		log.Print("运行日志字段超出长度限制")
		return
	}
	_, err := s.DB.ExecContext(task, `INSERT INTO runtime_logs(level,module,event,account_id,message,status,duration_ms,created_at,last_at,bucket)
 VALUES(?,?,?,?,?,?,?,?,?,?) ON CONFLICT(level,module,event,account_id,message,status,bucket)
 DO UPDATE SET occurrences=occurrences+1,last_at=excluded.last_at,duration_ms=excluded.duration_ms`, entry.Level, entry.Module, entry.Event, entry.AccountID, entry.Message, entry.Status, entry.DurationMS, now, now, now/300)
	if err != nil {
		log.Print("运行日志保存失败")
	}
}

func (s *Store) RuntimeLogs(ctx context.Context, f RuntimeLogFilter, page, size int) (RuntimeLogPage, error) {
	out := RuntimeLogPage{Items: []RuntimeLog{}, Page: page, PageSize: size}
	if page < 1 || page > 1000000 || size < 1 || size > 100 {
		return out, errors.New("invalid pagination")
	}
	where := []string{"last_at>=?"}
	args := []any{time.Now().Add(-7 * 24 * time.Hour).Unix()}
	for _, v := range []struct{ column, value string }{{"level", f.Level}, {"module", f.Module}} {
		if v.value != "" {
			where = append(where, v.column+"=?")
			args = append(args, v.value)
		}
	}
	for _, v := range []struct {
		condition string
		value     int64
	}{{"account_id=?", f.AccountID}, {"last_at>=?", f.Since}, {"last_at<=?", f.Until}} {
		if v.value > 0 {
			where = append(where, v.condition)
			args = append(args, v.value)
		}
	}
	clause := " FROM runtime_logs WHERE " + strings.Join(where, " AND ")
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return out, err
	}
	defer tx.Rollback()
	if err = tx.QueryRowContext(ctx, "SELECT count(*)"+clause, args...).Scan(&out.Total); err != nil {
		return out, err
	}
	args = append(args, size, (page-1)*size)
	rows, err := tx.QueryContext(ctx, `SELECT id,level,module,event,account_id,message,status,duration_ms,occurrences,created_at,last_at`+clause+` ORDER BY last_at DESC,id DESC LIMIT ? OFFSET ?`, args...)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var e RuntimeLog
		if err = rows.Scan(&e.ID, &e.Level, &e.Module, &e.Event, &e.AccountID, &e.Message, &e.Status, &e.DurationMS, &e.Occurrences, &e.CreatedAt, &e.LastAt); err != nil {
			return out, err
		}
		out.Items = append(out.Items, e)
	}
	if err = rows.Err(); err != nil {
		return out, err
	}
	rows.Close()
	return out, tx.Commit()
}
func (s *Store) CleanupRuntimeLogs(ctx context.Context) error {
	_, err := s.DB.ExecContext(ctx, `DELETE FROM runtime_logs WHERE last_at<?`, time.Now().Add(-7*24*time.Hour).Unix())
	return err
}
