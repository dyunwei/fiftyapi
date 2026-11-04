package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

var ErrQuota = errors.New("额度不足，请等待重置或联系管理员")

type Window struct {
	ID        int64  `json:"id"`
	Kind      string `json:"kind"`
	StartedAt *int64 `json:"started_at"`
	ExpiresAt *int64 `json:"expires_at"`
	Used      int64  `json:"used"`
	Reserved  int64  `json:"reserved"`
	Limit     int64  `json:"limit"`
}
type Charge struct {
	ID          string `json:"id"`
	UserID      int64  `json:"user_id"`
	KeyID       int64  `json:"key_id"`
	ModelID     string `json:"model_id"`
	Reservation int64  `json:"reservation"`
	Amount      int64  `json:"amount"`
	Status      string `json:"status"`
	Error       string `json:"error"`
	CreatedAt   int64  `json:"created_at"`
}

func activeWindow(ctx context.Context, tx *sql.Tx, user int64, kind string, now int64) (Window, error) {
	var w Window
	w.Kind = kind
	err := tx.QueryRowContext(ctx, "SELECT id,started_at,expires_at,used,reserved FROM quota_windows WHERE user_id=? AND kind=? AND active=1", user, kind).Scan(&w.ID, &w.StartedAt, &w.ExpiresAt, &w.Used, &w.Reserved)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return w, err
	}
	if err == nil && (w.ExpiresAt == nil || *w.ExpiresAt > now) {
		return w, nil
	}
	if err == nil {
		if _, err = tx.ExecContext(ctx, "UPDATE quota_windows SET active=0 WHERE id=?", w.ID); err != nil {
			return w, err
		}
	}
	r, err := tx.ExecContext(ctx, "INSERT INTO quota_windows(user_id,kind) VALUES(?,?)", user, kind)
	if err != nil {
		return w, err
	}
	id, _ := r.LastInsertId()
	return Window{ID: id, Kind: kind}, nil
}

func (s *Store) Reserve(ctx context.Context, id string, user, key int64, model string, p Price, estimate int64, now time.Time) error {
	if estimate <= 0 {
		return errors.New("预占金额必须为正数")
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var limit5, limit7 int64
	err = tx.QueryRowContext(ctx, `SELECT u.limit_5h,u.limit_7d FROM users u JOIN api_keys k ON k.user_id=u.id WHERE u.id=? AND k.id=? AND u.enabled=1 AND k.enabled=1`, user, key).Scan(&limit5, &limit7)
	if err != nil {
		return errors.New("用户或 API Key 不可用")
	}
	w5, err := activeWindow(ctx, tx, user, "5h", now.Unix())
	if err != nil {
		return err
	}
	w7, err := activeWindow(ctx, tx, user, "7d", now.Unix())
	if err != nil {
		return err
	}
	available5 := limit5 - w5.Used - w5.Reserved
	available7 := limit7 - w7.Used - w7.Reserved
	if available5 <= 0 || available7 <= 0 {
		return ErrQuota
	}
	reserve := min(estimate, available5, available7)
	for _, w := range []Window{w5, w7} {
		if _, err = tx.ExecContext(ctx, "UPDATE quota_windows SET reserved=reserved+? WHERE id=?", reserve, w.ID); err != nil {
			return err
		}
	}
	snapshot, _ := json.Marshal(p)
	if _, err = tx.ExecContext(ctx, `INSERT INTO request_charges(id,user_id,key_id,model_id,window_5h,window_7d,reservation,status,price_snapshot,created_at) VALUES(?,?,?,?,?,?,?,'inflight',?,?)`, id, user, key, model, w5.ID, w7.ID, reserve, string(snapshot), now.Unix()); err != nil {
		return err
	}
	return tx.Commit()
}
func (s *Store) Settle(ctx context.Context, id string, amount int64, now time.Time) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = settleTx(ctx, tx, id, amount, now); err != nil {
		return err
	}
	return tx.Commit()
}
func settleTx(ctx context.Context, tx *sql.Tx, id string, amount int64, now time.Time) error {
	if amount < 0 || amount > 1000000000000000 {
		return errors.New("结算金额超出范围")
	}
	var err error
	var w5, w7, reserve, oldAmount int64
	var status string
	if err = tx.QueryRowContext(ctx, "SELECT window_5h,window_7d,reservation,amount,status FROM request_charges WHERE id=?", id).Scan(&w5, &w7, &reserve, &oldAmount, &status); err != nil {
		return err
	}
	if status == "settled" {
		if amount != oldAmount {
			return errors.New("请求已按不同金额结算")
		}
		return nil
	}
	for _, item := range []struct {
		id       int64
		duration time.Duration
	}{{w5, 5 * time.Hour}, {w7, 7 * 24 * time.Hour}} {
		r, err := tx.ExecContext(ctx, "UPDATE quota_windows SET reserved=reserved-?,used=used+? WHERE id=? AND reserved>=?", reserve, amount, item.id, reserve)
		if err != nil {
			return err
		}
		n, _ := r.RowsAffected()
		if n != 1 {
			return errors.New("额度预占记录不一致")
		}
		if amount > 0 {
			if _, err = tx.ExecContext(ctx, "UPDATE quota_windows SET started_at=?,expires_at=? WHERE id=? AND started_at IS NULL", now.Unix(), now.Add(item.duration).Unix(), item.id); err != nil {
				return err
			}
		}
	}
	if _, err = tx.ExecContext(ctx, "UPDATE request_charges SET amount=?,status='settled',settled_at=? WHERE id=?", amount, now.Unix(), id); err != nil {
		return err
	}
	return nil
}
func (s *Store) Pending(ctx context.Context, id string) error {
	_, err := s.DB.ExecContext(ctx, "UPDATE request_charges SET status='pending' WHERE id=? AND status='inflight'", id)
	return err
}
func (s *Store) RecoverInFlight(ctx context.Context) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, "UPDATE request_charges SET status='pending' WHERE status='inflight'"); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `INSERT OR IGNORE INTO request_logs(id,user_id,key_id,model,provider,status,duration_ms,error,created_at) SELECT c.id,c.user_id,c.key_id,c.model_id,COALESCE(m.provider,''),503,0,'服务重启时请求未完成',c.created_at FROM request_charges c LEFT JOIN models m ON m.id=c.model_id WHERE c.status='pending'`); err != nil {
		return err
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	// Startup only, while the server file lock is held. Existing legacy pending
	// requests follow the same policy; currently executing requests must never be
	// passed through this recovery routine by a background maintenance worker.
	rows, err := s.DB.QueryContext(ctx, "SELECT id FROM request_charges WHERE status='pending' ORDER BY id")
	if err != nil {
		return err
	}
	ids := []string{}
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, id := range ids {
		if err = s.autoSettleRequest(ctx, id); err != nil {
			s.noteSettlementFailure(ctx, id)
			if ctx.Err() != nil {
				return ctx.Err()
			}
		}
	}
	return nil
}
func (s *Store) Quota(ctx context.Context, user int64) ([]Window, error) {
	var limit5, limit7 int64
	if err := s.DB.QueryRowContext(ctx, "SELECT limit_5h,limit_7d FROM users WHERE id=?", user).Scan(&limit5, &limit7); err != nil {
		return nil, err
	}
	result := []Window{}
	now := time.Now().Unix()
	for _, kind := range []string{"5h", "7d"} {
		w := Window{Kind: kind, Limit: limit5}
		if kind == "7d" {
			w.Limit = limit7
		}
		err := s.DB.QueryRowContext(ctx, "SELECT id,started_at,expires_at,used,reserved FROM quota_windows WHERE user_id=? AND kind=? AND active=1 AND (expires_at IS NULL OR expires_at>?)", user, kind, now).Scan(&w.ID, &w.StartedAt, &w.ExpiresAt, &w.Used, &w.Reserved)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return nil, err
		}
		result = append(result, w)
	}
	return result, nil
}
func (s *Store) SetLimits(ctx context.Context, actor, user, limit5, limit7 int64) error {
	if limit5 < 0 || limit7 < 0 || limit5 > 1000000000000000 || limit7 > 1000000000000000 {
		return errors.New("额度超出范围")
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	r, err := tx.ExecContext(ctx, "UPDATE users SET limit_5h=?,limit_7d=? WHERE id=?", limit5, limit7, user)
	if err != nil {
		return err
	}
	n, _ := r.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	details, _ := json.Marshal(map[string]int64{"limit_5h": limit5, "limit_7d": limit7})
	if _, err = tx.ExecContext(ctx, "INSERT INTO admin_audit_logs(actor_id,action,target_id,details,created_at) VALUES(?,'set_limits',?,?,?)", actor, user, string(details), time.Now().Unix()); err != nil {
		return err
	}
	return tx.Commit()
}
func (s *Store) ResetQuota(ctx context.Context, actor, user int64, kind string) error {
	if kind != "5h" && kind != "7d" && kind != "both" {
		return errors.New("窗口类型无效")
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, k := range []string{"5h", "7d"} {
		if kind != "both" && kind != k {
			continue
		}
		if _, err = tx.ExecContext(ctx, "UPDATE quota_windows SET active=0 WHERE user_id=? AND kind=? AND active=1", user, k); err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, "INSERT INTO quota_windows(user_id,kind) VALUES(?,?)", user, k); err != nil {
			return err
		}
	}
	if _, err = tx.ExecContext(ctx, "INSERT INTO admin_audit_logs(actor_id,action,target_id,details,created_at) VALUES(?,'reset_quota',?,?,?)", actor, user, kind, time.Now().Unix()); err != nil {
		return err
	}
	return tx.Commit()
}
func (s *Store) PendingCharges(ctx context.Context) ([]Charge, error) {
	rows, err := s.DB.QueryContext(ctx, "SELECT c.id,c.user_id,c.key_id,c.model_id,c.reservation,c.amount,c.status,c.created_at,COALESCE(NULLIF(l.error,''),'自动结算失败，请核查账本或日志') FROM request_charges c LEFT JOIN request_logs l ON l.id=c.id WHERE c.status='pending' ORDER BY c.created_at DESC,c.id DESC LIMIT 200")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []Charge{}
	for rows.Next() {
		var c Charge
		if err = rows.Scan(&c.ID, &c.UserID, &c.KeyID, &c.ModelID, &c.Reservation, &c.Amount, &c.Status, &c.CreatedAt, &c.Error); err != nil {
			return nil, err
		}
		result = append(result, c)
	}
	return result, rows.Err()
}
func (s *Store) ManualSettle(ctx context.Context, actor int64, id string, amount int64, reason string) error {
	if len(reason) < 3 || len(reason) > 200 {
		return errors.New("请填写 3–200 字节的核算依据")
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var status string
	if err = tx.QueryRowContext(ctx, "SELECT status FROM request_charges WHERE id=?", id).Scan(&status); err != nil {
		return err
	}
	if status != "pending" {
		return errors.New("只能核算待处理请求")
	}
	if err = ensureChargeLogTx(ctx, tx, id); err != nil {
		return err
	}
	if err = settleTx(ctx, tx, id, amount, time.Now()); err != nil {
		return err
	}
	details, _ := json.Marshal(map[string]any{"request_id": id, "amount": amount, "reason": reason})
	if _, err = tx.ExecContext(ctx, "INSERT INTO admin_audit_logs(actor_id,action,details,created_at) VALUES(?,'manual_settle',?,?)", actor, string(details), time.Now().Unix()); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, "UPDATE request_logs SET amount=?,error=? WHERE id=?", amount, "已人工核算："+reason, id); err != nil {
		return err
	}
	return tx.Commit()
}
func (s *Store) PriceSnapshot(ctx context.Context, id string) (Price, error) {
	var raw string
	var p Price
	err := s.DB.QueryRowContext(ctx, "SELECT price_snapshot FROM request_charges WHERE id=?", id).Scan(&raw)
	if err != nil {
		return p, err
	}
	return decodePriceSnapshot(raw)
}

func decodePriceSnapshot(raw string) (Price, error) {
	var p Price
	var fields map[string]json.RawMessage
	if json.Unmarshal([]byte(raw), &fields) != nil {
		return p, fmt.Errorf("价格快照无效")
	}
	for _, name := range []string{"input", "output", "cached", "cache_write"} {
		if len(fields[name]) == 0 || string(fields[name]) == "null" {
			return p, fmt.Errorf("价格快照不完整")
		}
	}
	if json.Unmarshal([]byte(raw), &p) != nil {
		return p, fmt.Errorf("价格快照无效")
	}
	if _, err := p.Cost(TokenUsage{}); err != nil {
		return p, err
	}
	return p, nil
}
