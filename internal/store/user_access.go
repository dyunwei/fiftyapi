package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

// Nil Models allows all priced models. An empty, non-nil list allows none.
// Concurrency zero delegates the limit to upstream account scheduling.
type UserAccess struct {
	Models      []string `json:"models"`
	Concurrency int      `json:"concurrency"`
	GroupID     *int64   `json:"group_id"`
}

func (p UserAccess) Allows(model string) bool {
	if p.Models == nil {
		return true
	}
	for _, id := range p.Models {
		if id == model {
			return true
		}
	}
	return false
}

type accessQuerier interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func readUserAccess(ctx context.Context, q accessQuerier, user int64) (UserAccess, error) {
	var out UserAccess
	var raw string
	err := q.QueryRowContext(ctx, "SELECT models,concurrency,group_id FROM user_access WHERE user_id=?", user).Scan(&raw, &out.Concurrency, &out.GroupID)
	if errors.Is(err, sql.ErrNoRows) {
		return out, nil
	}
	if err != nil {
		return out, err
	}
	err = json.Unmarshal([]byte(raw), &out.Models)
	return out, err
}
func (s *Store) UserAccess(ctx context.Context, user int64) (UserAccess, error) {
	return readUserAccess(ctx, s.DB, user)
}
func (s *Store) SetUserAccess(ctx context.Context, actor, user int64, p UserAccess) error {
	if p.Concurrency < 0 || p.Concurrency > 100 || len(p.Models) > 1000 {
		return errors.New("用户访问限制无效")
	}
	seen := map[string]bool{}
	for _, model := range p.Models {
		if model == "" || len(model) > 255 || strings.TrimSpace(model) != model || seen[model] {
			return errors.New("模型列表无效或重复")
		}
		seen[model] = true
	}
	raw, _ := json.Marshal(p.Models)
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	// Omitted group_id preserves membership for existing API clients. Zero clears it.
	updateGroup := p.GroupID != nil
	if updateGroup && *p.GroupID == 0 {
		p.GroupID = nil
	}
	if p.GroupID != nil {
		if err = validAccessGroup(ctx, tx, *p.GroupID); err != nil {
			return err
		}
	}
	err = tx.QueryRowContext(ctx, `INSERT INTO user_access(user_id,models,concurrency,group_id) VALUES(?,?,?,?)
 ON CONFLICT(user_id) DO UPDATE SET models=excluded.models,concurrency=excluded.concurrency,
 group_id=CASE WHEN ? THEN excluded.group_id ELSE user_access.group_id END
 RETURNING group_id`, user, string(raw), p.Concurrency, p.GroupID, updateGroup).Scan(&p.GroupID)
	if err != nil {
		return err
	}
	details, _ := json.Marshal(p)
	if _, err = tx.ExecContext(ctx, "INSERT INTO admin_audit_logs(actor_id,action,target_id,details,created_at) VALUES(?,'user_access',?,?,?)", actor, user, string(details), time.Now().Unix()); err != nil {
		return err
	}
	return tx.Commit()
}

var ErrUserConcurrency = errors.New("用户并发已达到上限")
var ErrModelAccess = errors.New("用户无权使用该模型")
