package store

import (
	"context"
	"time"
)

func (s *Store) GatewaySession(ctx context.Context, hash []byte) (int64, error) {
	id, _, err := s.GatewaySessionBinding(ctx, hash)
	return id, err
}

func (s *Store) GatewaySessionBinding(ctx context.Context, hash []byte) (int64, int64, error) {
	var id, hold int64
	err := s.DB.QueryRowContext(ctx, "SELECT account_id,hold_until FROM gateway_sessions WHERE session_hash=? AND expires_at>?", hash, time.Now().Unix()).Scan(&id, &hold)
	return id, hold, err
}

func (s *Store) BindGatewaySession(ctx context.Context, user, account int64, hash []byte) (int64, error) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	now := time.Now().Unix()
	if _, err = tx.ExecContext(ctx, "DELETE FROM gateway_sessions WHERE session_hash=? AND expires_at<=?", hash, now); err != nil {
		return 0, err
	}
	if _, err = tx.ExecContext(ctx, "INSERT OR IGNORE INTO gateway_sessions(session_hash,user_id,account_id,expires_at) VALUES(?,?,?,?)", hash, user, account, time.Now().Add(7*24*time.Hour).Unix()); err != nil {
		return 0, err
	}
	if _, err = tx.ExecContext(ctx, "UPDATE gateway_sessions SET expires_at=? WHERE session_hash=?", time.Now().Add(7*24*time.Hour).Unix(), hash); err != nil {
		return 0, err
	}
	var bound int64
	if err = tx.QueryRowContext(ctx, "SELECT account_id FROM gateway_sessions WHERE session_hash=?", hash).Scan(&bound); err != nil {
		return 0, err
	}
	return bound, tx.Commit()
}

// ClaimGatewaySession is a compare-and-swap: concurrent turns cannot silently
// overwrite a newer binding. Portable histories may move from the observed
// previous account; account-bound histories must keep their owner.
func (s *Store) ClaimGatewaySession(ctx context.Context, user, account int64, hash []byte, previous int64, portable bool) (int64, error) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	now := time.Now().Unix()
	if _, err = tx.ExecContext(ctx, "DELETE FROM gateway_sessions WHERE session_hash=? AND expires_at<=?", hash, now); err != nil {
		return 0, err
	}
	if _, err = tx.ExecContext(ctx, "INSERT OR IGNORE INTO gateway_sessions(session_hash,user_id,account_id,expires_at) VALUES(?,?,?,?)", hash, user, account, now+7*24*3600); err != nil {
		return 0, err
	}
	if portable && previous > 0 {
		if _, err = tx.ExecContext(ctx, "UPDATE gateway_sessions SET account_id=? WHERE session_hash=? AND account_id=? AND user_id=? AND hold_until<=?", account, hash, previous, user, now); err != nil {
			return 0, err
		}
	}
	var bound int64
	if err = tx.QueryRowContext(ctx, "SELECT account_id FROM gateway_sessions WHERE session_hash=? AND user_id=?", hash, user).Scan(&bound); err != nil {
		return 0, err
	}
	if bound == account {
		if _, err = tx.ExecContext(ctx, "UPDATE gateway_sessions SET expires_at=? WHERE session_hash=?", now+7*24*3600, hash); err != nil {
			return 0, err
		}
	}
	return bound, tx.Commit()
}
