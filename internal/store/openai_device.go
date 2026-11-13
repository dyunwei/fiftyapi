package store

import (
	"context"
	"database/sql"
	"errors"

	"github.com/google/uuid"
)

// OpenAIDeviceID belongs to the account row, independently of replaceable OAuth
// credentials. Concurrent first access converges on one persisted random ID.
func (s *Store) OpenAIDeviceID(ctx context.Context, account int64) (string, error) {
	var id string
	err := s.DB.QueryRowContext(ctx, "SELECT device_id FROM openai_account_devices WHERE account_id=?", account).Scan(&id)
	if err == nil {
		return id, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return "", err
	}
	if _, err = s.DB.ExecContext(ctx, `INSERT OR IGNORE INTO openai_account_devices(account_id,device_id)
 SELECT id,? FROM upstream_accounts WHERE id=? AND provider='openai' AND deleted_at IS NULL`, uuid.NewString(), account); err != nil {
		return "", err
	}
	err = s.DB.QueryRowContext(ctx, "SELECT device_id FROM openai_account_devices WHERE account_id=?", account).Scan(&id)
	return id, err
}
