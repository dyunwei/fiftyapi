package store

import (
	"context"
	"time"
)

type ContributionLink struct {
	ID        int64  `json:"id"`
	ActorID   int64  `json:"-"`
	Provider  string `json:"provider"`
	Note      string `json:"note"`
	CreatedAt int64  `json:"created_at"`
	ExpiresAt int64  `json:"expires_at"`
	RevokedAt *int64 `json:"revoked_at"`
	Count     int    `json:"contributed_count"`
}

// AccountOrigin is set by the server, never decoded from an import request.
type AccountOrigin struct {
	LinkID      int64
	Fingerprint []byte
}

func (s *Store) CreateContributionLink(ctx context.Context, actor int64, hash []byte, provider, note string, expires time.Time) (int64, error) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	r, err := tx.ExecContext(ctx, "INSERT INTO account_contribution_links(token_hash,actor_id,provider,note,created_at,expires_at) VALUES(?,?,?,?,?,?)", hash, actor, provider, note, time.Now().Unix(), expires.Unix())
	if err != nil {
		return 0, err
	}
	id, err := r.LastInsertId()
	if err != nil {
		return 0, err
	}
	if err = audit(ctx, tx, actor, "create_contribution_link", id); err != nil {
		return 0, err
	}
	return id, tx.Commit()
}
func (s *Store) ContributionLinks(ctx context.Context) ([]ContributionLink, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT l.id,l.provider,l.note,l.created_at,l.expires_at,l.revoked_at,(SELECT count(*) FROM upstream_accounts a WHERE a.contribution_link_id=l.id AND a.deleted_at IS NULL) FROM account_contribution_links l ORDER BY l.id DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ContributionLink{}
	for rows.Next() {
		var l ContributionLink
		if err = rows.Scan(&l.ID, &l.Provider, &l.Note, &l.CreatedAt, &l.ExpiresAt, &l.RevokedAt, &l.Count); err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}
func (s *Store) ContributionLink(ctx context.Context, hash []byte) (ContributionLink, error) {
	var l ContributionLink
	err := s.DB.QueryRowContext(ctx, `SELECT l.id,l.actor_id,l.provider,l.expires_at FROM account_contribution_links l JOIN users u ON u.id=l.actor_id WHERE l.token_hash=? AND l.revoked_at IS NULL AND l.expires_at>? AND u.enabled=1 AND u.role='admin'`, hash, time.Now().Unix()).Scan(&l.ID, &l.ActorID, &l.Provider, &l.ExpiresAt)
	return l, err
}
func (s *Store) RevokeContributionLink(ctx context.Context, actor, id int64) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	r, err := tx.ExecContext(ctx, "UPDATE account_contribution_links SET revoked_at=COALESCE(revoked_at,?) WHERE id=?", time.Now().Unix(), id)
	if err != nil {
		return err
	}
	n, _ := r.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	if err = audit(ctx, tx, actor, "revoke_contribution_link", id); err != nil {
		return err
	}
	return tx.Commit()
}
