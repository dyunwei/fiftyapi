package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

var ErrInvitation = errors.New("邀请链接无效、已过期或已使用，请联系管理员重新邀请")
var ErrInviteCooldown = errors.New("邀请正在发送或刚刚发送，请稍后再试")

type Invitation struct {
	UserID      int64
	Email       string
	DisplayName string
	ExpiresAt   int64
}

func (s *Store) CreateInvitedUser(ctx context.Context, actor int64, email, name string, limit5, limit7 int64, tokenHash []byte, now time.Time) (int64, error) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	r, err := tx.ExecContext(ctx, "INSERT INTO users(username,email,display_name,password_hash,password_set,role,limit_5h,limit_7d,created_at) VALUES(?,?,?,'!invitation-pending',0,'user',?,?,?)", email, email, name, limit5, limit7, now.Unix())
	if err != nil {
		return 0, err
	}
	id, err := r.LastInsertId()
	if err != nil {
		return 0, err
	}
	if _, err = tx.ExecContext(ctx, "INSERT INTO user_invitations(user_id,token_hash,expires_at,issued_at,mail_state) VALUES(?,?,?,?,'sending')", id, tokenHash, now.Add(24*time.Hour).Unix(), now.Unix()); err != nil {
		return 0, err
	}
	if err = audit(ctx, tx, actor, "invite_user", id); err != nil {
		return 0, err
	}
	return id, tx.Commit()
}

func (s *Store) RenewInvitation(ctx context.Context, actor, target int64, hash []byte, now time.Time) (Invitation, error) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return Invitation{}, err
	}
	defer tx.Rollback()
	var out Invitation
	out.UserID = target
	err = tx.QueryRowContext(ctx, "SELECT email,display_name FROM users WHERE id=? AND role='user' AND enabled=1 AND email IS NOT NULL", target).Scan(&out.Email, &out.DisplayName)
	if err != nil {
		return out, ErrNotFound
	}
	var issued int64
	var state string
	err = tx.QueryRowContext(ctx, "SELECT issued_at,mail_state FROM user_invitations WHERE user_id=?", target).Scan(&issued, &state)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return out, err
	}
	if err == nil && state != "failed" && issued > now.Add(-time.Minute).Unix() {
		return out, ErrInviteCooldown
	}
	out.ExpiresAt = now.Add(24 * time.Hour).Unix()
	if _, err = tx.ExecContext(ctx, "INSERT INTO user_invitations(user_id,token_hash,expires_at,issued_at,mail_state) VALUES(?,?,?,?,'sending') ON CONFLICT(user_id) DO UPDATE SET token_hash=excluded.token_hash,expires_at=excluded.expires_at,issued_at=excluded.issued_at,accepted_at=NULL,mail_state='sending',mail_error=''", target, hash, out.ExpiresAt, now.Unix()); err != nil {
		return out, err
	}
	if err = audit(ctx, tx, actor, "resend_invitation", target); err != nil {
		return out, err
	}
	return out, tx.Commit()
}

func (s *Store) Invitation(ctx context.Context, hash []byte, now time.Time) (Invitation, error) {
	var out Invitation
	err := s.DB.QueryRowContext(ctx, "SELECT u.id,u.email,u.display_name,i.expires_at FROM user_invitations i JOIN users u ON u.id=i.user_id WHERE i.token_hash=? AND i.accepted_at IS NULL AND i.expires_at>? AND u.enabled=1 AND u.role='user'", hash, now.Unix()).Scan(&out.UserID, &out.Email, &out.DisplayName, &out.ExpiresAt)
	if errors.Is(err, sql.ErrNoRows) {
		err = ErrInvitation
	}
	return out, err
}
func (s *Store) InvitationDelivery(ctx context.Context, user int64, hash []byte, sent bool) error {
	state, message := "sent", ""
	if !sent {
		state = "failed"
		message = "邮件未确认发送，请检查 SMTP 配置后重试"
	}
	_, err := s.DB.ExecContext(ctx, "UPDATE user_invitations SET mail_state=?,mail_error=? WHERE user_id=? AND token_hash=? AND accepted_at IS NULL", state, message, user, hash)
	return err
}
func (s *Store) AcceptInvitation(ctx context.Context, hash []byte, passwordHash string, now time.Time) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var user int64
	err = tx.QueryRowContext(ctx, "SELECT u.id FROM user_invitations i JOIN users u ON u.id=i.user_id WHERE i.token_hash=? AND i.accepted_at IS NULL AND i.expires_at>? AND u.enabled=1 AND u.role='user'", hash, now.Unix()).Scan(&user)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrInvitation
	}
	if err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, "UPDATE user_invitations SET accepted_at=? WHERE user_id=?", now.Unix(), user); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, "UPDATE users SET password_hash=?,password_set=1 WHERE id=?", passwordHash, user); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, "DELETE FROM sessions WHERE user_id=?", user); err != nil {
		return err
	}
	if err = audit(ctx, tx, user, "accept_invitation", user); err != nil {
		return err
	}
	return tx.Commit()
}
