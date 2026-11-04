package store

import (
	"context"
	"errors"
	"time"
)

var ErrUserExists = errors.New("邮箱或账号已存在")
var ErrRegistrationUnavailable = errors.New("请先完成管理员初始化")

// RegisterUser always creates a regular member with zero quota. The supplied
// real name uses the existing display_name column and is visible to admins.
func (s *Store) RegisterUser(ctx context.Context, email, realName, hash string) (int64, error) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	var initialized bool
	if err = tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM users WHERE role='admin')").Scan(&initialized); err != nil {
		return 0, err
	}
	if !initialized {
		return 0, ErrRegistrationUnavailable
	}
	var exists bool
	if err = tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM users WHERE lower(username)=? OR lower(email)=?)", email, email).Scan(&exists); err != nil {
		return 0, err
	}
	if exists {
		return 0, ErrUserExists
	}
	r, err := tx.ExecContext(ctx, `INSERT INTO users(username,email,display_name,password_hash,password_set,role,enabled,limit_5h,limit_7d,created_at) VALUES(?,?,?,?,1,'user',1,0,0,?)`, email, email, realName, hash, time.Now().Unix())
	if err != nil {
		return 0, err
	}
	id, err := r.LastInsertId()
	if err != nil {
		return 0, err
	}
	if err = audit(ctx, tx, id, "register_user", id); err != nil {
		return 0, err
	}
	return id, tx.Commit()
}
