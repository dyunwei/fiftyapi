package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"
	"unicode/utf8"
)

var ErrAccountAccess = errors.New("用户无权使用该模型的上游账号")

type AccessGroup struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

type AccountAccess struct {
	Mode     string  `json:"mode"`
	GroupIDs []int64 `json:"group_ids"`
}

// Callers bind the user's ID and alias the upstream account as a.
const accountAccessPredicate = `(a.group_restricted=0 OR EXISTS(
 SELECT 1 FROM user_access ua JOIN account_access_groups ag ON ag.group_id=ua.group_id
 WHERE ua.user_id=? AND ag.account_id=a.id
))`

func (s *Store) AccessGroups(ctx context.Context) ([]AccessGroup, error) {
	rows, err := s.DB.QueryContext(ctx, "SELECT id,name FROM access_groups ORDER BY name COLLATE NOCASE,id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	groups := []AccessGroup{}
	for rows.Next() {
		var group AccessGroup
		if err := rows.Scan(&group.ID, &group.Name); err != nil {
			return nil, err
		}
		groups = append(groups, group)
	}
	return groups, rows.Err()
}

func (s *Store) SetAccessGroup(ctx context.Context, actor, id int64, name string) (AccessGroup, error) {
	name = strings.TrimSpace(name)
	if name == "" || utf8.RuneCountInString(name) > 64 {
		return AccessGroup{}, errors.New("分组名称须为 1–64 个字符")
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return AccessGroup{}, err
	}
	defer tx.Rollback()
	var duplicate bool
	if err = tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM access_groups WHERE name=? AND id<>?)", name, id).Scan(&duplicate); err != nil {
		return AccessGroup{}, err
	}
	if duplicate {
		return AccessGroup{}, errors.New("访问分组名称已存在")
	}
	action := "rename_access_group"
	if id == 0 {
		action = "create_access_group"
		err = tx.QueryRowContext(ctx, "INSERT INTO access_groups(name) VALUES(?) RETURNING id", name).Scan(&id)
	} else {
		err = tx.QueryRowContext(ctx, "UPDATE access_groups SET name=? WHERE id=? RETURNING id", name, id).Scan(&id)
	}
	if errors.Is(err, sql.ErrNoRows) {
		return AccessGroup{}, ErrNotFound
	}
	if err != nil {
		return AccessGroup{}, err
	}
	if err = audit(ctx, tx, actor, action, id); err != nil {
		return AccessGroup{}, err
	}
	return AccessGroup{ID: id, Name: name}, tx.Commit()
}

func (s *Store) DeleteAccessGroup(ctx context.Context, actor, id int64) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	err = tx.QueryRowContext(ctx, "DELETE FROM access_groups WHERE id=? RETURNING id", id).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if err = audit(ctx, tx, actor, "delete_access_group", id); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) AccountAccess(ctx context.Context, account int64) (AccountAccess, error) {
	var access AccountAccess
	var raw string
	err := s.DB.QueryRowContext(ctx, `SELECT CASE WHEN a.group_restricted=1 THEN 'groups' ELSE 'public' END,
 (SELECT json_group_array(group_id) FROM (SELECT group_id FROM account_access_groups WHERE account_id=a.id ORDER BY group_id))
 FROM upstream_accounts a WHERE a.id=? AND a.deleted_at IS NULL`, account).Scan(&access.Mode, &raw)
	if errors.Is(err, sql.ErrNoRows) {
		return access, ErrNotFound
	}
	if err != nil {
		return access, err
	}
	return access, json.Unmarshal([]byte(raw), &access.GroupIDs)
}

func (s *Store) AccountAllowsUser(ctx context.Context, account, user int64) (bool, error) {
	var allowed bool
	err := s.DB.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM upstream_accounts a WHERE a.id=? AND a.deleted_at IS NULL AND `+accountAccessPredicate+`)`, account, user).Scan(&allowed)
	return allowed, err
}

func validAccessGroup(ctx context.Context, tx *sql.Tx, group int64) error {
	var exists bool
	if err := tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM access_groups WHERE id=?)", group).Scan(&exists); err != nil {
		return err
	}
	if !exists {
		return errors.New("访问分组不存在")
	}
	return nil
}

func (s *Store) SetAccountAccess(ctx context.Context, actor, account int64, access AccountAccess) error {
	if (access.Mode != "public" && access.Mode != "groups") || (access.Mode == "public" && len(access.GroupIDs) > 0) {
		return errors.New("上游账号访问范围无效")
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	err = tx.QueryRowContext(ctx, "UPDATE upstream_accounts SET group_restricted=? WHERE id=? AND deleted_at IS NULL RETURNING id", access.Mode == "groups", account).Scan(&account)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, "DELETE FROM account_access_groups WHERE account_id=?", account); err != nil {
		return err
	}
	seen := map[int64]bool{}
	for _, group := range access.GroupIDs {
		if seen[group] {
			return errors.New("访问分组重复")
		}
		seen[group] = true
		if err = validAccessGroup(ctx, tx, group); err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, "INSERT INTO account_access_groups(account_id,group_id) VALUES(?,?)", account, group); err != nil {
			return err
		}
	}
	details, _ := json.Marshal(access)
	if _, err = tx.ExecContext(ctx, "INSERT INTO admin_audit_logs(actor_id,action,target_id,details,created_at) VALUES(?,'set_account_access',?,?,?)", actor, account, string(details), time.Now().Unix()); err != nil {
		return err
	}
	return tx.Commit()
}
