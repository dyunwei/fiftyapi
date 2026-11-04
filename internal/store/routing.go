package store

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

type AccountPolicy struct {
	Priority    int `json:"priority"`
	Weight      int `json:"weight"`
	Concurrency int `json:"concurrency"`
	MaxQueue    int `json:"max_queue"`
	WaitMS      int `json:"wait_ms"`
}

func DefaultAccountPolicy() AccountPolicy {
	return AccountPolicy{Priority: 50, Weight: 1, Concurrency: 5, MaxQueue: 16, WaitMS: 2000}
}
func (p AccountPolicy) Validate() error {
	if p.Priority < 0 || p.Priority > 100 || p.Weight < 1 || p.Weight > 100 || p.Concurrency < 1 || p.Concurrency > 100 || p.MaxQueue < 0 || p.MaxQueue > 1000 || p.WaitMS < 0 || p.WaitMS > 30000 {
		return errors.New("调度参数超出范围")
	}
	return nil
}
func (s *Store) AccountPolicy(ctx context.Context, id int64) (AccountPolicy, error) {
	var p AccountPolicy
	err := s.DB.QueryRowContext(ctx, "SELECT priority,weight,concurrency,max_queue,wait_ms FROM account_policies WHERE account_id=?", id).Scan(&p.Priority, &p.Weight, &p.Concurrency, &p.MaxQueue, &p.WaitMS)
	return p, err
}
func (s *Store) SetAccountPolicy(ctx context.Context, actor, id int64, p AccountPolicy) error {
	if err := p.Validate(); err != nil {
		return err
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, "UPDATE account_policies SET initialized=1,priority=?,weight=?,concurrency=?,max_queue=?,wait_ms=? WHERE account_id=?", p.Priority, p.Weight, p.Concurrency, p.MaxQueue, p.WaitMS, id)
	if err != nil {
		return err
	}
	n, _ := result.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	detail, _ := json.Marshal(p)
	if _, err = tx.ExecContext(ctx, "INSERT INTO admin_audit_logs(actor_id,action,target_id,details,created_at) VALUES(?,'update_account_policy',?,?,?)", actor, id, string(detail), time.Now().Unix()); err != nil {
		return err
	}
	return tx.Commit()
}

type AccountHealth struct {
	AccountID       int64             `json:"account_id"`
	Failures        int               `json:"consecutive_failures"`
	ErrorRate       float64           `json:"error_rate"`
	FirstOutputMS   float64           `json:"first_output_ms"`
	LastStatus      int               `json:"last_status"`
	LastSuccess     *int64            `json:"last_success"`
	LastFailure     *int64            `json:"last_failure"`
	Reason          string            `json:"reason"`
	PrimaryUsed     *float64          `json:"primary_used_percent"`
	PrimaryReset    *int64            `json:"primary_reset_at"`
	SecondaryUsed   *float64          `json:"secondary_used_percent"`
	SecondaryReset  *int64            `json:"secondary_reset_at"`
	QuotaObservedAt *int64            `json:"quota_observed_at"`
	Cooldowns       []AccountCooldown `json:"cooldowns"`
}
type AccountCooldown struct {
	Scope  string `json:"scope"`
	Until  int64  `json:"until"`
	Reason string `json:"reason"`
}
type RouteCandidate struct {
	ID                       int64
	Policy                   AccountPolicy
	Failures                 int
	ErrorRate, FirstOutputMS float64
	CooldownUntil            int64
}

func (s *Store) RouteCandidates(ctx context.Context, model, scope string, protocol ...string) ([]RouteCandidate, error) {
	legacyScope := scope
	if strings.HasPrefix(scope, "generate:") || strings.HasPrefix(scope, "generate/") {
		legacyScope = "generate:" + model
	}
	requested := ""
	if len(protocol) > 0 {
		requested = protocol[0]
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT a.id,p.priority,p.weight,p.concurrency,p.max_queue,p.wait_ms,h.failures,h.error_rate,h.first_output_ms,COALESCE((SELECT max(until_at) FROM account_cooldowns c WHERE c.account_id=a.id AND c.scope IN ('',?,?)),0)
 FROM upstream_accounts a JOIN account_models am ON am.account_id=a.id JOIN models m ON m.id=am.model_id AND m.enabled=1 JOIN account_policies p ON p.account_id=a.id JOIN account_health h ON h.account_id=a.id
 WHERE am.model_id=? AND a.enabled=1 AND a.state IN ('catalog_ready','ready') AND (?='' OR EXISTS(SELECT 1 FROM json_each(m.protocols) WHERE value=?)) ORDER BY p.priority,a.id`, scope, legacyScope, model, requested, requested)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []RouteCandidate{}
	for rows.Next() {
		var c RouteCandidate
		if err = rows.Scan(&c.ID, &c.Policy.Priority, &c.Policy.Weight, &c.Policy.Concurrency, &c.Policy.MaxQueue, &c.Policy.WaitMS, &c.Failures, &c.ErrorRate, &c.FirstOutputMS, &c.CooldownUntil); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}
func (s *Store) AccountHealth(ctx context.Context, id int64) (AccountHealth, error) {
	h := AccountHealth{AccountID: id, Cooldowns: []AccountCooldown{}}
	err := s.DB.QueryRowContext(ctx, "SELECT failures,error_rate,first_output_ms,last_status,last_success,last_failure,last_reason,primary_used,primary_reset,secondary_used,secondary_reset,quota_observed_at FROM account_health WHERE account_id=?", id).Scan(&h.Failures, &h.ErrorRate, &h.FirstOutputMS, &h.LastStatus, &h.LastSuccess, &h.LastFailure, &h.Reason, &h.PrimaryUsed, &h.PrimaryReset, &h.SecondaryUsed, &h.SecondaryReset, &h.QuotaObservedAt)
	if err != nil {
		return h, err
	}
	rows, err := s.DB.QueryContext(ctx, "SELECT scope,until_at,reason FROM account_cooldowns WHERE account_id=? AND until_at>? ORDER BY scope", id, time.Now().Unix())
	if err != nil {
		return h, err
	}
	defer rows.Close()
	for rows.Next() {
		var c AccountCooldown
		if err = rows.Scan(&c.Scope, &c.Until, &c.Reason); err != nil {
			return h, err
		}
		h.Cooldowns = append(h.Cooldowns, c)
	}
	return h, rows.Err()
}

type RequestAttempt struct {
	RequestID     string `json:"request_id"`
	Attempt       int    `json:"attempt"`
	AccountID     int64  `json:"account_id"`
	Model         string `json:"model"`
	Status        int    `json:"status"`
	DurationMS    int64  `json:"duration_ms"`
	FirstOutputMS *int64 `json:"first_output_ms"`
	Dispatched    bool   `json:"dispatched"`
	Retryable     bool   `json:"retryable"`
	Reason        string `json:"reason"`
	CreatedAt     int64  `json:"created_at"`
}
type AccountObservation struct {
	RequestAttempt
	Success, Failure             bool
	CooldownUntil                int64
	CooldownScope                string
	PrimaryUsed, SecondaryUsed   *float64
	PrimaryReset, SecondaryReset *int64
}

// RecordAttempt persists routing evidence and health together. It never stores
// a body, prompt, arbitrary upstream error, or credential.
func (s *Store) RecordAttempt(ctx context.Context, o AccountObservation) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	_, err = tx.ExecContext(ctx, `INSERT INTO request_attempts(request_id,attempt,account_id,model,status,duration_ms,first_output_ms,dispatched,retryable,reason,created_at) VALUES(?,?,?,?,?,?,?,?,?,?,?)`, o.RequestID, o.Attempt, o.AccountID, o.Model, o.Status, o.DurationMS, o.FirstOutputMS, o.Dispatched, o.Retryable, o.Reason, o.CreatedAt)
	if err != nil {
		return err
	}
	var failures int
	var rate, first float64
	if err = tx.QueryRowContext(ctx, "SELECT failures,error_rate,first_output_ms FROM account_health WHERE account_id=?", o.AccountID).Scan(&failures, &rate, &first); err != nil {
		return err
	}
	now := time.Now().Unix()
	if o.Success {
		failures = 0
		rate *= .8
	} else if o.Failure {
		failures++
		rate = rate*.8 + .2
	}
	if o.FirstOutputMS != nil {
		if first == 0 {
			first = float64(*o.FirstOutputMS)
		} else {
			first = first*.8 + float64(*o.FirstOutputMS)*.2
		}
	}
	_, err = tx.ExecContext(ctx, `UPDATE account_health SET failures=?,error_rate=?,first_output_ms=?,last_status=?,last_reason=?,last_success=CASE WHEN ? THEN ? ELSE last_success END,last_failure=CASE WHEN ? THEN ? ELSE last_failure END WHERE account_id=?`, failures, rate, first, o.Status, o.Reason, o.Success, now, o.Failure || o.CooldownUntil > now, now, o.AccountID)
	if err != nil {
		return err
	}
	if o.PrimaryUsed != nil || o.SecondaryUsed != nil {
		_, err = tx.ExecContext(ctx, `UPDATE account_health SET primary_used=?,primary_reset=?,secondary_used=?,secondary_reset=?,quota_observed_at=? WHERE account_id=?`, o.PrimaryUsed, o.PrimaryReset, o.SecondaryUsed, o.SecondaryReset, now, o.AccountID)
		if err != nil {
			return err
		}
	}
	if o.Failure && failures >= 3 && o.CooldownUntil < now+30 {
		o.CooldownUntil = now + 30
		o.CooldownScope = ""
		o.Reason = "连续失败，短暂熔断"
	}
	if o.CooldownUntil > now {
		_, err = tx.ExecContext(ctx, `INSERT INTO account_cooldowns(account_id,scope,until_at,reason) VALUES(?,?,?,?) ON CONFLICT(account_id,scope) DO UPDATE SET until_at=max(until_at,excluded.until_at),reason=CASE WHEN excluded.until_at>=until_at THEN excluded.reason ELSE reason END`, o.AccountID, o.CooldownScope, o.CooldownUntil, o.Reason)
		if err != nil {
			return err
		}
	}
	return tx.Commit()
}
func (s *Store) ResetAccountHealth(ctx context.Context, actor, id int64) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	r, err := tx.ExecContext(ctx, "UPDATE account_health SET failures=0,error_rate=0,last_reason='管理员恢复调度，等待下次请求验证' WHERE account_id=?", id)
	if err != nil {
		return err
	}
	n, _ := r.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	if _, err = tx.ExecContext(ctx, "DELETE FROM account_cooldowns WHERE account_id=?", id); err != nil {
		return err
	}
	if err = audit(ctx, tx, actor, "reset_account_health", id); err != nil {
		return err
	}
	return tx.Commit()
}
func (s *Store) RequestAttempts(ctx context.Context, user int64, admin bool, id string) ([]RequestAttempt, error) {
	var owner int64
	if err := s.DB.QueryRowContext(ctx, "SELECT user_id FROM request_charges WHERE id=?", id).Scan(&owner); err != nil {
		return nil, ErrNotFound
	}
	if !admin && owner != user {
		return nil, ErrNotFound
	}
	rows, err := s.DB.QueryContext(ctx, "SELECT request_id,attempt,account_id,model,status,duration_ms,first_output_ms,dispatched,retryable,reason,created_at FROM request_attempts WHERE request_id=? ORDER BY attempt", id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []RequestAttempt{}
	for rows.Next() {
		var a RequestAttempt
		if err = rows.Scan(&a.RequestID, &a.Attempt, &a.AccountID, &a.Model, &a.Status, &a.DurationMS, &a.FirstOutputMS, &a.Dispatched, &a.Retryable, &a.Reason, &a.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// Migrate the original encrypted concurrency setting once. Explicit policy edits
// win, and metadata updates do not reset policy or active account health.
func (s *Store) InitializeAccountPolicy(ctx context.Context, id int64, concurrency int) error {
	if concurrency == 0 {
		concurrency = DefaultAccountPolicy().Concurrency
	}
	if concurrency < 1 || concurrency > 100 {
		return errors.New("并发配置超出范围")
	}
	_, err := s.DB.ExecContext(ctx, "UPDATE account_policies SET concurrency=?,initialized=1 WHERE account_id=? AND initialized=0", concurrency, id)
	return err
}

func (s *Store) SaveQuotaSnapshot(ctx context.Context, id int64, primary *float64, primaryReset *int64, secondary *float64, secondaryReset *int64) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	now := time.Now().Unix()
	r, err := tx.ExecContext(ctx, "UPDATE account_health SET primary_used=?,primary_reset=?,secondary_used=?,secondary_reset=?,quota_observed_at=? WHERE account_id=?", primary, primaryReset, secondary, secondaryReset, now, id)
	if err != nil {
		return err
	}
	n, _ := r.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	until := int64(0)
	for _, pair := range []struct {
		used  *float64
		reset *int64
	}{{primary, primaryReset}, {secondary, secondaryReset}} {
		if pair.used != nil && *pair.used >= 100 {
			next := now + 60
			if pair.reset != nil && *pair.reset > now {
				next = *pair.reset
			}
			if next > until {
				until = next
			}
		}
	}
	if until > now {
		if _, err = tx.ExecContext(ctx, `INSERT INTO account_cooldowns(account_id,scope,until_at,reason) VALUES(?,'',?,'上游额度耗尽，等待窗口重置') ON CONFLICT(account_id,scope) DO UPDATE SET until_at=max(until_at,excluded.until_at),reason=CASE WHEN excluded.until_at>=until_at THEN excluded.reason ELSE reason END`, id, until); err != nil {
			return err
		}
	}
	return tx.Commit()
}
