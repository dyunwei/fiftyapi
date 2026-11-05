package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// DefaultMemberBudget is $100 in each independent rolling budget window.
const DefaultMemberBudget int64 = 100_000_000

var ErrInitialized = errors.New("管理员已初始化")
var ErrNotFound = errors.New("记录不存在")

type User struct {
	Email               *string `json:"email,omitempty"`
	DisplayName         string  `json:"display_name"`
	PasswordSet         bool    `json:"password_set"`
	InvitationState     string  `json:"invitation_state,omitempty"`
	InvitationExpiresAt *int64  `json:"invitation_expires_at,omitempty"`
	MailState           string  `json:"mail_state,omitempty"`
	MailError           string  `json:"mail_error,omitempty"`
	ID                  int64   `json:"id"`
	Username            string  `json:"username"`
	Role                string  `json:"role"`
	Enabled             bool    `json:"enabled"`
	Limit5h             int64   `json:"limit_5h_microusd"`
	Limit7d             int64   `json:"limit_7d_microusd"`
	PasswordHash        string  `json:"-"`
}

func (s *Store) Initialized(ctx context.Context) (bool, error) {
	var n int
	err := s.DB.QueryRowContext(ctx, "SELECT count(*) FROM users WHERE role='admin'").Scan(&n)
	return n > 0, err
}
func (s *Store) CreateAdmin(ctx context.Context, username, hash string) (int64, error) {
	return s.CreateAdminWithMigration(ctx, username, hash, false)
}

// CreateAdminWithMigration persists onboarding intent in the same transaction.
func (s *Store) CreateAdminWithMigration(ctx context.Context, username, hash string, migration bool) (int64, error) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	r, err := tx.ExecContext(ctx, `INSERT INTO users(username,password_hash,role,created_at,limit_5h,limit_7d) SELECT ?,?,'admin',?,?,? WHERE NOT EXISTS(SELECT 1 FROM users WHERE role='admin')`, username, hash, time.Now().Unix(), DefaultMemberBudget, DefaultMemberBudget)
	if err != nil {
		return 0, err
	}
	n, _ := r.RowsAffected()
	if n == 0 {
		return 0, ErrInitialized
	}
	id, _ := r.LastInsertId()
	if migration {
		if _, err = tx.ExecContext(ctx, "INSERT INTO settings(key,value) VALUES('sub2api_onboarding_pending','1') ON CONFLICT(key) DO UPDATE SET value=excluded.value"); err != nil {
			return 0, err
		}
	}
	if err = audit(ctx, tx, id, "setup_admin", id); err != nil {
		return 0, err
	}
	return id, tx.Commit()
}
func scanUser(row interface{ Scan(...any) error }) (User, error) {
	var u User
	err := row.Scan(userScan(&u)...)
	if errors.Is(err, sql.ErrNoRows) {
		err = ErrNotFound
	}
	return u, err
}

func userScan(u *User) []any {
	return []any{&u.ID, &u.Username, &u.PasswordHash, &u.Role, &u.Enabled, &u.Limit5h, &u.Limit7d, &u.Email, &u.DisplayName, &u.PasswordSet}
}

const userColumns = "id,username,password_hash,role,enabled,limit_5h,limit_7d,email,display_name,password_set"

func (s *Store) UserByName(ctx context.Context, name string) (User, error) {
	return scanUser(s.DB.QueryRowContext(ctx, "SELECT "+userColumns+" FROM users WHERE username=?", name))
}
func (s *Store) Users(ctx context.Context) ([]User, error) {
	rows, err := s.DB.QueryContext(ctx, "SELECT "+userColumns+",CASE WHEN i.accepted_at IS NOT NULL THEN 'accepted' WHEN i.expires_at<=? THEN 'expired' WHEN i.user_id IS NOT NULL THEN 'pending' ELSE '' END,i.expires_at,COALESCE(i.mail_state,''),COALESCE(i.mail_error,'') FROM users LEFT JOIN user_invitations i ON i.user_id=users.id ORDER BY id", time.Now().Unix())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []User{}
	for rows.Next() {
		var u User
		err := rows.Scan(append(userScan(&u), &u.InvitationState, &u.InvitationExpiresAt, &u.MailState, &u.MailError)...)
		if err != nil {
			return nil, err
		}
		result = append(result, u)
	}
	return result, rows.Err()
}
func (s *Store) CreateUser(ctx context.Context, actor int64, name, hash string, limit5h, limit7d int64) (int64, error) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	r, err := tx.ExecContext(ctx, "INSERT INTO users(username,password_hash,role,limit_5h,limit_7d,created_at) VALUES(?,?,'user',?,?,?)", name, hash, limit5h, limit7d, time.Now().Unix())
	if err != nil {
		return 0, err
	}
	id, _ := r.LastInsertId()
	if err = audit(ctx, tx, actor, "create_user", id); err != nil {
		return 0, err
	}
	return id, tx.Commit()
}
func (s *Store) EnableUser(ctx context.Context, actor, target int64, enabled bool) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	// This first slice only changes regular users, never disables the sole admin.
	r, err := tx.ExecContext(ctx, "UPDATE users SET enabled=? WHERE id=? AND role='user'", enabled, target)
	if err != nil {
		return err
	}
	n, _ := r.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	if !enabled {
		if _, err = tx.ExecContext(ctx, "DELETE FROM user_invitations WHERE user_id=?", target); err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, "DELETE FROM sessions WHERE user_id=?", target); err != nil {
			return err
		}
	}
	if err = audit(ctx, tx, actor, "set_user_enabled", target); err != nil {
		return err
	}
	return tx.Commit()
}
func (s *Store) SaveSession(ctx context.Context, hash []byte, user int64, csrf string, expiry time.Time) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, "DELETE FROM sessions WHERE expires_at<=?", time.Now().Unix()); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, "INSERT INTO sessions(token_hash,user_id,csrf_token,expires_at) VALUES(?,?,?,?)", hash, user, csrf, expiry.Unix()); err != nil {
		return err
	}
	return tx.Commit()
}
func (s *Store) Session(ctx context.Context, hash []byte) (User, string, error) {
	var csrf string
	var u User
	err := s.DB.QueryRowContext(ctx, `SELECT u.id,u.username,u.password_hash,u.role,u.enabled,u.limit_5h,u.limit_7d,u.email,u.display_name,u.password_set,s.csrf_token FROM sessions s JOIN users u ON u.id=s.user_id WHERE s.token_hash=? AND s.expires_at>? AND u.enabled=1 AND u.password_set=1`, hash, time.Now().Unix()).Scan(append(userScan(&u), &csrf)...)
	if errors.Is(err, sql.ErrNoRows) {
		err = ErrNotFound
	}
	return u, csrf, err
}
func (s *Store) DeleteSession(ctx context.Context, hash []byte) error {
	_, err := s.DB.ExecContext(ctx, "DELETE FROM sessions WHERE token_hash=?", hash)
	return err
}
func audit(ctx context.Context, tx *sql.Tx, actor int64, action string, target int64) error {
	_, err := tx.ExecContext(ctx, "INSERT INTO admin_audit_logs(actor_id,action,target_id,created_at) VALUES(?,?,?,?)", actor, action, target, time.Now().Unix())
	return err
}

func (s *Store) ChangePassword(ctx context.Context, actor, target int64, hash string, own bool) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	query := "UPDATE users SET password_hash=?,password_set=1 WHERE id=?"
	if !own {
		query += " AND role='user'"
	}
	r, err := tx.ExecContext(ctx, query, hash, target)
	if err != nil {
		return err
	}
	n, _ := r.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	if _, err = tx.ExecContext(ctx, "DELETE FROM user_invitations WHERE user_id=?", target); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, "DELETE FROM sessions WHERE user_id=?", target); err != nil {
		return err
	}
	if err = audit(ctx, tx, actor, "change_password", target); err != nil {
		return err
	}
	return tx.Commit()
}
