package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

const unknownUsageNote = "未取得可靠用量，按 0 结算并释放预占"
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
	var status, snapshot, message string
	var input, output, cached, write sql.NullInt64
	err = tx.QueryRowContext(ctx, `SELECT c.status,c.price_snapshot,l.input_tokens,l.output_tokens,l.cached_tokens,l.cache_write_tokens,COALESCE(l.error,'') FROM request_charges c LEFT JOIN request_logs l ON l.id=c.id WHERE c.id=?`, id).Scan(&status, &snapshot, &input, &output, &cached, &write, &message)
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
	amount := int64(0)
	message = strings.ReplaceAll(message, settlementFailureNote, "此前自动结算失败，本次已恢复")
	message = strings.ReplaceAll(message, "结算失败，需要管理员核算", "此前结算失败")
	message = strings.ReplaceAll(message, "用量待核算", "用量此前未确认")
	known := input.Valid && output.Valid && input.Int64 >= 0 && output.Int64 >= 0 && cached.Int64 >= 0 && write.Int64 >= 0
	if known {
		price, priceErr := decodePriceSnapshot(snapshot)
		if priceErr != nil {
			return priceErr
		}
		amount, err = price.Cost(TokenUsage{Input: input.Int64, Output: output.Int64, Cached: cached.Int64, CacheWrite: write.Int64})
		if err != nil {
			return err
		}
		if message != "" {
			message = appendAccountingNote(message, observedUsageNote)
		}
	} else {
		message = appendAccountingNote(message, unknownUsageNote)
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
