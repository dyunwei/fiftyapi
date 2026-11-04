package store

import (
	"context"
	"errors"
	"strings"
	"time"
)

type Account struct {
	ModelCount         int    `json:"model_count"`
	ModelsSyncedAt     *int64 `json:"models_synced_at"`
	Platform           string `json:"gateway_platform,omitempty"`
	ContributionLinkID *int64 `json:"contribution_link_id,omitempty"`
	ContributedAt      *int64 `json:"contributed_at,omitempty"`
	ID                 int64  `json:"id"`
	Name               string `json:"name"`
	Provider           string `json:"provider"`
	Enabled            bool   `json:"enabled"`
	State              string `json:"state"`
	LastError          string `json:"last_error,omitempty"`
	Credentials        []byte `json:"-"`
}

func (s *Store) Accounts(ctx context.Context) ([]Account, error) {
	rows, err := s.DB.QueryContext(ctx, "SELECT id,name,provider,enabled,state,last_error,gateway_platform,(SELECT count(*) FROM account_models WHERE account_id=upstream_accounts.id),models_synced_at,contribution_link_id,CASE WHEN contribution_link_id IS NOT NULL THEN created_at END FROM upstream_accounts WHERE deleted_at IS NULL ORDER BY id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []Account{}
	for rows.Next() {
		var a Account
		if err = rows.Scan(&a.ID, &a.Name, &a.Provider, &a.Enabled, &a.State, &a.LastError, &a.Platform, &a.ModelCount, &a.ModelsSyncedAt, &a.ContributionLinkID, &a.ContributedAt); err != nil {
			return nil, err
		}
		result = append(result, a)
	}
	return result, rows.Err()
}
func (s *Store) Account(ctx context.Context, id int64) (Account, error) {
	var a Account
	err := s.DB.QueryRowContext(ctx, "SELECT id,name,provider,enabled,state,last_error,credentials,gateway_platform FROM upstream_accounts WHERE id=? AND deleted_at IS NULL", id).Scan(&a.ID, &a.Name, &a.Provider, &a.Enabled, &a.State, &a.LastError, &a.Credentials, &a.Platform)
	return a, err
}
func (s *Store) CreateAccount(ctx context.Context, actor int64, name, provider string, seal func(int64) ([]byte, error), platforms ...string) (int64, error) {
	platform := ""
	if len(platforms) > 0 {
		platform = platforms[0]
	}
	return s.CreateAccountWithOrigin(ctx, actor, name, provider, seal, platform, AccountOrigin{})
}
func (s *Store) CreateAccountWithOrigin(ctx context.Context, actor int64, name, provider string, seal func(int64) ([]byte, error), platform string, origin AccountOrigin) (int64, error) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	now := time.Now().Unix()
	var linkID any
	enabled := true
	if origin.LinkID != 0 {
		var valid int
		if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM account_contribution_links l JOIN users u ON u.id=l.actor_id WHERE l.id=? AND l.actor_id=? AND l.provider=? AND l.revoked_at IS NULL AND l.expires_at>? AND u.enabled=1 AND u.role='admin'`, origin.LinkID, actor, provider, now).Scan(&valid); err != nil {
			return 0, err
		}
		if valid != 1 {
			return 0, ErrNotFound
		}
		linkID = origin.LinkID
		enabled = false
	}
	result, err := tx.ExecContext(ctx, "INSERT INTO upstream_accounts(name,provider,credentials,state,created_at,updated_at,gateway_platform,enabled,contribution_link_id,auth_fingerprint) VALUES(?,?,?,'authorized',?,?,?,?,?,?)", name, provider, []byte{}, now, now, platform, enabled, linkID, origin.Fingerprint)
	if err != nil {
		return 0, err
	}
	id, _ := result.LastInsertId()
	ciphertext, err := seal(id)
	if err != nil {
		return 0, err
	}
	if _, err = tx.ExecContext(ctx, "UPDATE upstream_accounts SET credentials=? WHERE id=?", ciphertext, id); err != nil {
		return 0, err
	}
	if _, err = tx.ExecContext(ctx, "INSERT INTO account_policies(account_id,concurrency) VALUES(?,?)", id, DefaultAccountPolicy().Concurrency); err != nil {
		return 0, err
	}
	if _, err = tx.ExecContext(ctx, "INSERT INTO account_health(account_id) VALUES(?)", id); err != nil {
		return 0, err
	}
	if err = audit(ctx, tx, actor, "import_upstream_account", id); err != nil {
		return 0, err
	}
	return id, tx.Commit()
}
func (s *Store) UpdateAccountCredentials(ctx context.Context, id int64, ciphertext []byte) error {
	result, err := s.DB.ExecContext(ctx, "UPDATE upstream_accounts SET credentials=?,updated_at=? WHERE id=? AND deleted_at IS NULL", ciphertext, time.Now().Unix(), id)
	if err != nil {
		return err
	}
	n, _ := result.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}
func (s *Store) AccountState(ctx context.Context, id int64, state, message string) error {
	result, err := s.DB.ExecContext(ctx, "UPDATE upstream_accounts SET state=?,last_error=?,updated_at=? WHERE id=? AND deleted_at IS NULL", state, message, time.Now().Unix(), id)
	if err != nil {
		return err
	}
	n, _ := result.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}
func (s *Store) EnableAccount(ctx context.Context, actor, id int64, enabled bool) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, "UPDATE upstream_accounts SET enabled=?,updated_at=? WHERE id=? AND deleted_at IS NULL", enabled, time.Now().Unix(), id)
	if err != nil {
		return err
	}
	n, _ := result.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	if err = audit(ctx, tx, actor, "set_account_enabled", id); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) RenameAccount(ctx context.Context, actor, id int64, name string) error {
	name = strings.TrimSpace(name)
	if name == "" || len(name) > 128 {
		return errors.New("账号名称无效")
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	r, err := tx.ExecContext(ctx, "UPDATE upstream_accounts SET name=?,updated_at=? WHERE id=? AND deleted_at IS NULL", name, time.Now().Unix(), id)
	if err != nil {
		return err
	}
	n, _ := r.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	if err = audit(ctx, tx, actor, "rename_upstream_account", id); err != nil {
		return err
	}
	return tx.Commit()
}
func (s *Store) DeleteAccount(ctx context.Context, actor, id int64) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	r, err := tx.ExecContext(ctx, "UPDATE upstream_accounts SET enabled=0,state='error',credentials=randomblob(32),name='deleted-'||id||'-'||hex(randomblob(8)),last_error='',deleted_at=?,updated_at=? WHERE id=? AND deleted_at IS NULL", time.Now().Unix(), time.Now().Unix(), id)
	if err != nil {
		return err
	}
	n, _ := r.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	for _, q := range []string{"DELETE FROM account_models WHERE account_id=?", "DELETE FROM gateway_sessions WHERE account_id=?", "DELETE FROM account_cooldowns WHERE account_id=?"} {
		if _, err = tx.ExecContext(ctx, q, id); err != nil {
			return err
		}
	}
	if err = audit(ctx, tx, actor, "delete_upstream_account", id); err != nil {
		return err
	}
	return tx.Commit()
}
