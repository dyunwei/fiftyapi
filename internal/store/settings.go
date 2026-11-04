package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

func (s *Store) Setting(ctx context.Context, name string) (string, bool, error) {
	var value string
	err := s.DB.QueryRowContext(ctx, "SELECT value FROM settings WHERE key=?", name).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	return value, err == nil, err
}

// SaveSetting stores an opaque value and audits only the operation, never its
// contents (which may be encrypted credentials).
func (s *Store) SaveSetting(ctx context.Context, actor int64, name string, value *string) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if value == nil {
		_, err = tx.ExecContext(ctx, "DELETE FROM settings WHERE key=?", name)
	} else {
		_, err = tx.ExecContext(ctx, "INSERT INTO settings(key,value) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value", name, *value)
	}
	if err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, "INSERT INTO admin_audit_logs(actor_id,action,details,created_at) VALUES(?,'update_setting',?,?)", actor, name, time.Now().Unix()); err != nil {
		return err
	}
	return tx.Commit()
}
