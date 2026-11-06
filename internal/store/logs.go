package store

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"time"
)

// RequestInference records client settings before account inference policies.
// Empty values mean unspecified (or unavailable for historical logs).
type RequestInference struct {
	ReasoningEffort      string `json:"reasoning_effort,omitempty"`
	ThinkingType         string `json:"thinking_type,omitempty"`
	ThinkingBudgetTokens *int64 `json:"thinking_budget_tokens,omitempty"`
	Speed                string `json:"speed,omitempty"`
	ServiceTier          string `json:"service_tier,omitempty"`
}

type RequestLog struct {
	RequestInference
	ID               string `json:"id"`
	UserID           int64  `json:"user_id"`
	KeyID            int64  `json:"key_id"`
	AccountID        *int64 `json:"account_id"`
	Model            string `json:"model"`
	Provider         string `json:"provider"`
	Status           int    `json:"status"`
	Input            *int64 `json:"input_tokens"`
	Output           *int64 `json:"output_tokens"`
	Cached           *int64 `json:"cached_tokens"`
	CacheWrite       *int64 `json:"cache_write_tokens"`
	Amount           *int64 `json:"amount"`
	DurationMS       int64  `json:"duration_ms"`
	Error            string `json:"error,omitempty"`
	CreatedAt        int64  `json:"created_at"`
	SettlementStatus string `json:"settlement_status,omitempty"`
	Reservation      *int64 `json:"reservation,omitempty"`
}

const insertRequestLog = `INSERT OR IGNORE INTO request_logs(id,user_id,key_id,account_id,model,provider,status,input_tokens,output_tokens,cached_tokens,cache_write_tokens,amount,duration_ms,error,created_at,reasoning_effort,thinking_type,thinking_budget_tokens,speed,service_tier) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`

func logArguments(l RequestLog) []any {
	return []any{l.ID, l.UserID, l.KeyID, l.AccountID, l.Model, l.Provider, l.Status, l.Input, l.Output, l.Cached, l.CacheWrite, l.Amount, l.DurationMS, l.Error, l.CreatedAt, l.ReasoningEffort, l.ThinkingType, l.ThinkingBudgetTokens, l.Speed, l.ServiceTier}
}

func (s *Store) LogRequest(ctx context.Context, l RequestLog) error {
	_, err := s.DB.ExecContext(ctx, insertRequestLog, logArguments(l)...)
	return err
}
func (s *Store) Logs(ctx context.Context, user int64, admin bool, before int64, model string, status int) ([]RequestLog, error) {
	return s.LogsPage(ctx, user, admin, before, model, status, "", 0)
}
func (s *Store) LogsPage(ctx context.Context, user int64, admin bool, before int64, model string, status int, beforeID string, since int64, requestIDs ...string) ([]RequestLog, error) {
	query := `SELECT id,user_id,key_id,account_id,model,provider,status,input_tokens,output_tokens,cached_tokens,cache_write_tokens,amount,duration_ms,error,created_at,reasoning_effort,thinking_type,thinking_budget_tokens,speed,service_tier FROM request_logs WHERE 1=1`
	args := []any{}
	if len(requestIDs) > 0 && requestIDs[0] != "" {
		query += " AND id=?"
		args = append(args, requestIDs[0])
	}
	if !admin {
		query += " AND user_id=?"
		args = append(args, user)
	}
	if before > 0 {
		if beforeID != "" {
			query += " AND (created_at<? OR (created_at=? AND id<?))"
			args = append(args, before, before, beforeID)
		} else {
			query += " AND created_at<?"
			args = append(args, before)
		}
	}
	if since > 0 {
		query += " AND created_at>=?"
		args = append(args, since)
	}
	if model != "" {
		query += " AND model=?"
		args = append(args, model)
	}
	if status > 0 {
		query += " AND status=?"
		args = append(args, status)
	}
	query += " ORDER BY created_at DESC,id DESC LIMIT 200"
	rows, err := s.DB.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []RequestLog{}
	for rows.Next() {
		var l RequestLog
		if err = rows.Scan(&l.ID, &l.UserID, &l.KeyID, &l.AccountID, &l.Model, &l.Provider, &l.Status, &l.Input, &l.Output, &l.Cached, &l.CacheWrite, &l.Amount, &l.DurationMS, &l.Error, &l.CreatedAt, &l.ReasoningEffort, &l.ThinkingType, &l.ThinkingBudgetTokens, &l.Speed, &l.ServiceTier); err != nil {
			return nil, err
		}
		result = append(result, l)
	}
	return result, rows.Err()
}
func (s *Store) LogRetention(ctx context.Context) (int, error) {
	var raw string
	if err := s.DB.QueryRowContext(ctx, "SELECT value FROM settings WHERE key='log_retention_days'").Scan(&raw); err != nil {
		return 0, err
	}
	return strconv.Atoi(raw)
}
func (s *Store) SetRetention(ctx context.Context, actor int64, days int) error {
	if days < 1 || days > 3650 {
		return errors.New("日志保留期需为 1–3650 天")
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, "UPDATE settings SET value=? WHERE key='log_retention_days'", strconv.Itoa(days)); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, "INSERT INTO admin_audit_logs(actor_id,action,details,created_at) VALUES(?,'set_log_retention',?,?)", actor, strconv.Itoa(days), time.Now().Unix()); err != nil {
		return err
	}
	return tx.Commit()
}
func (s *Store) CleanupLogs(ctx context.Context) error {
	if _, err := s.DB.ExecContext(ctx, "DELETE FROM gateway_sessions WHERE expires_at<=?", time.Now().Unix()); err != nil {
		return err
	}
	days, err := s.LogRetention(ctx)
	if err != nil {
		return err
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	cutoff := time.Now().Add(-time.Duration(days) * 24 * time.Hour).Unix()
	candidates := `SELECT l.id FROM request_logs l WHERE l.created_at<? AND NOT EXISTS(SELECT 1 FROM request_charges c WHERE c.id=l.id AND c.status!='settled') ORDER BY l.created_at,l.id LIMIT 1000`
	if _, err = tx.ExecContext(ctx, "DELETE FROM request_attempts WHERE request_id IN ("+candidates+")", cutoff); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, "DELETE FROM request_logs WHERE id IN ("+candidates+")", cutoff); err != nil {
		return err
	}
	return tx.Commit()
}
func (s *Store) AuditLogs(ctx context.Context) ([]map[string]any, error) {
	rows, err := s.DB.QueryContext(ctx, "SELECT id,actor_id,action,target_id,details,created_at FROM admin_audit_logs ORDER BY id DESC LIMIT 200")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []map[string]any{}
	for rows.Next() {
		var id, created int64
		var actor, target *int64
		var action, details string
		if err = rows.Scan(&id, &actor, &action, &target, &details, &created); err != nil {
			return nil, err
		}
		var parsed any
		if json.Unmarshal([]byte(details), &parsed) != nil {
			parsed = details
		}
		result = append(result, map[string]any{"id": id, "actor_id": actor, "action": action, "target_id": target, "details": parsed, "created_at": created})
	}
	return result, rows.Err()
}
