package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

var errUnknownUsage = errors.New("未取得可靠用量")

const observedUsageNote = "已按记录的用量自动结算并释放预占"
const settlementFailureNote = "自动结算失败，需要管理员核算"

func appendAccountingNote(message, note string) string {
	if strings.Contains(message, note) {
		return message
	}
	if message == "" {
		return note
	}
	return message + "；" + note
}

// FinalizeRequest persists usage evidence before changing the ledger. A crash
// between these short transactions can then be recovered using the immutable
// price snapshot, without guessing from a reservation or charging twice.
func (s *Store) FinalizeRequest(ctx context.Context, l RequestLog) (err error) {
	defer func() {
		if err != nil {
			save, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Second)
			defer cancel()
			_ = s.Pending(save, l.ID)
			_ = s.ReleasePending(save, l.ID)
			s.noteSettlementFailure(save, l.ID)
		}
	}()
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var status string
	if err = tx.QueryRowContext(ctx, "SELECT status FROM request_charges WHERE id=?", l.ID).Scan(&status); err != nil {
		return err
	}
	if status == "settled" {
		return nil
	}
	l.Amount = nil
	if _, err = tx.ExecContext(ctx, insertRequestLog, logArguments(l)...); err != nil {
		return err
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	if err = s.autoSettleRequest(ctx, l.ID); err != nil {
		return err
	}
	return nil
}

func (s *Store) autoSettleRequest(ctx context.Context, id string) error {
	return s.autoSettleRequestAs(ctx, id, nil)
}

// RetrySettlement only retries ledger work; it never sends another model call.
func (s *Store) RetrySettlement(ctx context.Context, actor int64, id string) error {
	err := s.autoSettleRequestAs(ctx, id, &actor)
	if err != nil {
		s.noteSettlementFailure(ctx, id)
	}
	return err
}

func (s *Store) autoSettleRequestAs(ctx context.Context, id string, actor *int64) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var status, snapshot, message, billing string
	var input, output, cached, write sql.NullInt64
	err = tx.QueryRowContext(ctx, `SELECT c.status,c.price_snapshot,l.input_tokens,l.output_tokens,l.cached_tokens,l.cache_write_tokens,COALESCE(l.error,''),COALESCE(l.billing_usage,'{}') FROM request_charges c LEFT JOIN request_logs l ON l.id=c.id WHERE c.id=?`, id).Scan(&status, &snapshot, &input, &output, &cached, &write, &message, &billing)
	if err != nil {
		return err
	}
	if status == "settled" {
		return nil
	}
	if actor != nil && status != "pending" {
		return errors.New("只能重试结算异常请求")
	}
	if err = ensureChargeLogTx(ctx, tx, id); err != nil {
		return err
	}
	if message == "" && !input.Valid && !output.Valid {
		message = "未取得已保存的可靠用量"
	}
	message = strings.ReplaceAll(message, settlementFailureNote, "此前自动结算失败，本次已恢复")
	message = strings.ReplaceAll(message, "结算失败，需要管理员核算", "此前结算失败")
	message = strings.ReplaceAll(message, "用量待核算", "用量此前未确认")
	message = strings.ReplaceAll(message, "未取得可靠用量，已释放预占，等待管理员核算", "用量此前未确认")
	amount, usageErr := recordedUsageCost(snapshot, billing, input, output, cached, write)
	if usageErr != nil {
		if actor != nil {
			return usageErr
		}
		amount = 0
		if errors.Is(usageErr, errUnknownUsage) {
			message = appendAccountingNote(message, "未取得可靠用量，按 0 结算并释放预占")
		} else {
			message = appendAccountingNote(message, usageErr.Error())
			message = appendAccountingNote(message, "无法可靠计费，按 0 结算并释放预占")
		}
	} else if message != "" {
		message = appendAccountingNote(message, observedUsageNote)
	}
	if err = settleTx(ctx, tx, id, amount, time.Now()); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, "UPDATE request_logs SET amount=?,error=? WHERE id=?", amount, message, id); err != nil {
		return err
	}
	if actor != nil {
		details, _ := json.Marshal(map[string]any{"request_id": id, "amount": amount})
		if _, err = tx.ExecContext(ctx, "INSERT INTO admin_audit_logs(actor_id,action,details,created_at) VALUES(?,'retry_settle',?,?)", *actor, string(details), time.Now().Unix()); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func recordedUsageCost(snapshot, billing string, input, output, cached, write sql.NullInt64) (int64, error) {
	if !input.Valid || !output.Valid || input.Int64 < 0 || output.Int64 < 0 || cached.Int64 < 0 || write.Int64 < 0 {
		return 0, errUnknownUsage
	}
	price, err := decodePriceSnapshot(snapshot)
	if err != nil {
		return 0, err
	}
	var metadata BillingUsage
	if err := json.Unmarshal([]byte(billing), &metadata); err != nil {
		return 0, errors.New("计费用量元数据无效")
	}
	amount, err := price.Cost(TokenUsage{BillingUsage: metadata, Input: input.Int64, Output: output.Int64, Cached: cached.Int64, CacheWrite: write.Int64})
	if err != nil {
		return 0, err
	}
	if amount > maxSettlementAmount {
		return 0, errors.New("结算金额超出范围")
	}
	return amount, nil
}

func ensureChargeLogTx(ctx context.Context, tx *sql.Tx, id string) error {
	_, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO request_logs(id,user_id,key_id,model,provider,status,duration_ms,error,created_at) SELECT c.id,c.user_id,c.key_id,c.model_id,COALESCE(m.provider,''),0,0,'结算日志未完整保存，使用账本记录恢复',c.created_at FROM request_charges c LEFT JOIN models m ON m.id=c.model_id WHERE c.id=?`, id)
	return err
}

func (s *Store) noteSettlementFailure(ctx context.Context, id string) {
	save, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Second)
	defer cancel()
	// Never overwrite a concurrent administrator settlement or its audit reason.
	_, _ = s.DB.ExecContext(save, `UPDATE request_logs SET error=CASE WHEN instr(error,?)>0 THEN error WHEN error='' THEN ? ELSE error||'；'||? END WHERE id=? AND EXISTS(SELECT 1 FROM request_charges c WHERE c.id=request_logs.id AND c.status='pending')`, settlementFailureNote, settlementFailureNote, settlementFailureNote, id)
}

// ReleasePending preserves the usage and snapshot for review without holding
// reservations after a terminal request. Retrying settlement remains idempotent.
func (s *Store) ReleasePending(ctx context.Context, id string) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var w5, w7, reserve int64
	var status string
	if err = tx.QueryRowContext(ctx, "SELECT window_5h,window_7d,reservation,status FROM request_charges WHERE id=?", id).Scan(&w5, &w7, &reserve, &status); err != nil {
		return err
	}
	if status == "settled" {
		return nil
	}
	for _, w := range []int64{w5, w7} {
		r, e := tx.ExecContext(ctx, "UPDATE quota_windows SET reserved=reserved-? WHERE id=? AND reserved>=?", reserve, w, reserve)
		if e != nil {
			return e
		}
		n, _ := r.RowsAffected()
		if n != 1 {
			return errors.New("额度预占记录不一致")
		}
	}
	if _, err = tx.ExecContext(ctx, "UPDATE request_charges SET status='pending',reservation=0 WHERE id=?", id); err != nil {
		return err
	}
	return tx.Commit()
}
