package store

import (
	"context"
	"database/sql"
	"errors"

	"strings"
)

type ModelProtocolStatus struct {
	Protocol       string `json:"protocol"`
	CooldownUntil  int64  `json:"cooldown_until"`
	CooldownReason string `json:"cooldown_reason,omitempty"`
	Routable       bool   `json:"routable"`
}
type AccountModel struct {
	ProtocolStatus []ModelProtocolStatus `json:"protocol_status"`
	CooldownUntil  int64                 `json:"cooldown_until"`
	CooldownReason string                `json:"cooldown_reason,omitempty"`
	ID             string                `json:"id"`
	UpstreamID     string                `json:"upstream_id"`
	Protocol       string                `json:"protocol"`
	Protocols      []string              `json:"protocols"`
	Enabled        bool                  `json:"enabled"`
	Priced         bool                  `json:"priced"`
	Routable       bool                  `json:"routable"`
}
type AccountModelPage struct {
	AccountID int64          `json:"account_id"`
	Items     []AccountModel `json:"items"`
	Total     int            `json:"total"`
	Page      int            `json:"page"`
	PageSize  int            `json:"page_size"`
	SyncedAt  *int64         `json:"synced_at"`
}

// AccountModelsPage lists saved membership without invoking suppliers or filtering
// unpriced/disabled models. The snapshot belongs to this account, not all users
// of a shared model. Unknown legacy synchronization times stay unknown.
func (s *Store) AccountModelsPage(ctx context.Context, account int64, page, size int, search string) (AccountModelPage, error) {
	out := AccountModelPage{AccountID: account, Items: []AccountModel{}, Page: page, PageSize: size}
	if account <= 0 || page < 1 || page > 1000000 || size < 1 || size > 100 {
		return out, ErrNotFound
	}
	tx, err := s.DB.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return out, err
	}
	defer tx.Rollback()
	var enabled bool
	var state string
	err = tx.QueryRowContext(ctx, "SELECT enabled,state,models_synced_at FROM upstream_accounts WHERE id=? AND deleted_at IS NULL", account).Scan(&enabled, &state, &out.SyncedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return out, ErrNotFound
	}
	if err != nil {
		return out, err
	}
	from := ` FROM account_models am JOIN models m ON m.id=am.model_id WHERE am.account_id=?`
	args := []any{account}
	if q := strings.TrimSpace(search); q != "" {
		from += ` AND (instr(lower(m.id),lower(?))>0 OR instr(lower(am.upstream_id),lower(?))>0)`
		args = append(args, q, q)
	}
	if err = tx.QueryRowContext(ctx, "SELECT count(*)"+from, args...).Scan(&out.Total); err != nil {
		return out, err
	}
	params := append(append([]any{}, args...), size, (page-1)*size)
	rows, err := tx.QueryContext(ctx, `SELECT m.id,am.upstream_id,m.provider,am.protocols,m.enabled,EXISTS(SELECT 1 FROM model_prices p WHERE p.model_id=m.id)`+from+` ORDER BY m.id LIMIT ? OFFSET ?`, params...)
	if err != nil {
		return out, err
	}
	for rows.Next() {
		var m AccountModel
		var provider, protocols string
		if err = rows.Scan(&m.ID, &m.UpstreamID, &provider, &protocols, &m.Enabled, &m.Priced); err != nil {
			rows.Close()
			return out, err
		}
		model := Model{Provider: provider}
		model.setProtocols(protocols)
		m.Protocol = model.Protocol
		m.Protocols = model.Protocols
		m.Routable = enabled && (state == "catalog_ready" || state == "ready") && m.Enabled && m.Priced
		out.Items = append(out.Items, m)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return out, err
	}
	cooldownRows, e := tx.QueryContext(ctx, "SELECT scope,until_at,reason FROM account_cooldowns WHERE account_id=? AND until_at>unixepoch()", account)
	if e != nil {
		return out, e
	}
	cooldowns := map[string]AccountCooldown{}
	for cooldownRows.Next() {
		var c AccountCooldown
		if e = cooldownRows.Scan(&c.Scope, &c.Until, &c.Reason); e != nil {
			cooldownRows.Close()
			return out, e
		}
		cooldowns[c.Scope] = c
	}
	e = cooldownRows.Err()
	cooldownRows.Close()
	if e != nil {
		return out, e
	}

	for i := range out.Items {
		m := &out.Items[i]
		base := m.Routable
		m.Routable = false
		m.ProtocolStatus = []ModelProtocolStatus{}
		for _, protocol := range m.Protocols {
			c := cooldowns[""]
			for _, scope := range []string{"generate:" + m.ID, "generate/" + protocol + ":" + m.ID} {
				if scoped := cooldowns[scope]; scoped.Until > c.Until {
					c = scoped
				}
			}
			routable := base && c.Until == 0
			m.ProtocolStatus = append(m.ProtocolStatus, ModelProtocolStatus{Protocol: protocol, CooldownUntil: c.Until, CooldownReason: c.Reason, Routable: routable})
			m.Routable = m.Routable || routable
			if c.Until > 0 && (m.CooldownUntil == 0 || c.Until < m.CooldownUntil) {
				m.CooldownUntil = c.Until
				m.CooldownReason = c.Reason
			}
		}
		if m.Routable {
			m.CooldownUntil = 0
			m.CooldownReason = ""
		}
	}
	return out, tx.Commit()
}

// AccountModel returns an exact saved membership, including unpriced models.
func (s *Store) AccountModel(ctx context.Context, account int64, id string) (AccountModel, error) {
	var m AccountModel
	var provider, protocols string
	err := s.DB.QueryRowContext(ctx, `SELECT m.id,am.upstream_id,m.provider,am.protocols,m.enabled
 FROM account_models am JOIN models m ON m.id=am.model_id
 JOIN upstream_accounts a ON a.id=am.account_id
 WHERE am.account_id=? AND m.id=? AND a.deleted_at IS NULL`, account, id).Scan(&m.ID, &m.UpstreamID, &provider, &protocols, &m.Enabled)
	if errors.Is(err, sql.ErrNoRows) {
		return m, ErrNotFound
	}
	if err != nil {
		return m, err
	}
	model := Model{Provider: provider}
	model.setProtocols(protocols)
	m.Protocol = model.Protocol
	m.Protocols = model.Protocols
	return m, nil
}
