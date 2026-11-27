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
	err := q.QueryRowContext(ctx, `SELECT COALESCE(g.models,u.models),COALESCE(g.concurrency,u.concurrency),u.group_id FROM user_access u LEFT JOIN access_groups g ON g.id=u.group_id WHERE u.user_id=?`, user).Scan(&raw, &out.Concurrency, &out.GroupID)
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
func validateAccessPolicy(models []string, concurrency int) error {
	if concurrency < 0 || concurrency > 100 || len(models) > 1000 {
		return errors.New("用户访问限制无效")
	}
	seen := map[string]bool{}
	for _, model := range models {
		if model == "" || len(model) > 255 || strings.TrimSpace(model) != model || seen[model] {
			return errors.New("模型列表无效或重复")
		}
		seen[model] = true
	}
	return nil
}

func (s *Store) SetUserAccess(ctx context.Context, actor, user int64, p UserAccess) error {
	if err := validateAccessPolicy(p.Models, p.Concurrency); err != nil {
		return err
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

// An explicit move switches the member to the destination policy and drops
// their legacy individual limits. Saving the same group is a no-op.
func (s *Store) SetUserGroup(ctx context.Context, actor, user, group int64) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var current int64
	err = tx.QueryRowContext(ctx, `SELECT COALESCE(ua.group_id,0) FROM users u LEFT JOIN user_access ua ON ua.user_id=u.id WHERE u.id=?`, user).Scan(&current)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil || current == group {
		return err
	}
	if group != 0 {
		if err = validAccessGroup(ctx, tx, group); err != nil {
			return err
		}
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO user_access(user_id,group_id) VALUES(?,NULLIF(?,0))
 ON CONFLICT(user_id) DO UPDATE SET group_id=excluded.group_id,models='null',concurrency=0`, user, group); err != nil {
		return err
	}
	details, _ := json.Marshal(map[string]int64{"previous_group_id": current, "group_id": group})
	if _, err = tx.ExecContext(ctx, "INSERT INTO admin_audit_logs(actor_id,action,target_id,details,created_at) VALUES(?,'set_user_group',?,?,?)", actor, user, string(details), time.Now().Unix()); err != nil {
		return err
	}
	return tx.Commit()
}

var ErrUserConcurrency = errors.New("用户并发已达到上限")
var ErrModelAccess = errors.New("用户无权使用该模型")
