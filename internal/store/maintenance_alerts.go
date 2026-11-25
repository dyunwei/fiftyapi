package store

import (
	"context"
	"encoding/json"
)

const activeMaintenanceIssues = `
SELECT 'reauth' AS kind, 'reauth:' || id AS issue FROM upstream_accounts
 WHERE enabled=1 AND deleted_at IS NULL AND state='reauth_required'
UNION ALL
SELECT 'settlement', 'settlement:' || u.id FROM users u
 WHERE u.enabled=1 AND EXISTS (SELECT 1 FROM request_charges c WHERE c.user_id=u.id AND c.status='pending')
UNION ALL
SELECT 'backup', 'backup' WHERE (
 SELECT status FROM backup_records WHERE status IN ('completed','failed') ORDER BY started_at DESC,rowid DESC LIMIT 1
)='failed'`

type MaintenanceIssue struct {
	Kind string
	ID   string
}

func (s *Store) MaintenanceTodo(ctx context.Context) (map[string]int, error) {
	rows, err := s.DB.QueryContext(ctx, "SELECT kind,count(*) FROM ("+activeMaintenanceIssues+") GROUP BY kind")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	counts := map[string]int{}
	for rows.Next() {
		var kind string
		var count int
		if err := rows.Scan(&kind, &count); err != nil {
			return nil, err
		}
		counts[kind] = count
	}
	return counts, rows.Err()
}

func (s *Store) UnsentMaintenanceIssues(ctx context.Context, backupStale ...bool) ([]MaintenanceIssue, error) {
	active := activeMaintenanceIssues
	if len(backupStale) > 0 && backupStale[0] {
		active += " UNION ALL SELECT 'backup_stale', 'backup_stale'"
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, "DELETE FROM maintenance_alerts_sent WHERE issue NOT IN (SELECT issue FROM ("+active+"))"); err != nil {
		return nil, err
	}
	rows, err := tx.QueryContext(ctx, "SELECT kind,issue FROM ("+active+") WHERE issue NOT IN (SELECT issue FROM maintenance_alerts_sent)")
	if err != nil {
		return nil, err
	}
	var issues []MaintenanceIssue
	for rows.Next() {
		var item MaintenanceIssue
		if err := rows.Scan(&item.Kind, &item.ID); err != nil {
			rows.Close()
			return nil, err
		}
		issues = append(issues, item)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	return issues, tx.Commit()
}

func (s *Store) MarkMaintenanceIssuesSent(ctx context.Context, issues []MaintenanceIssue) error {
	ids := make([]string, 0, len(issues))
	for _, issue := range issues {
		ids = append(ids, issue.ID)
	}
	raw, err := json.Marshal(ids)
	if err != nil {
		return err
	}
	_, err = s.DB.ExecContext(ctx, "INSERT OR IGNORE INTO maintenance_alerts_sent(issue) SELECT value FROM json_each(?)", string(raw))
	return err
}
