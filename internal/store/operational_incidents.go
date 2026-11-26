package store

import (
	"context"
	"time"
)

type IncidentTransition struct {
	Model, Protocol, Cause string
	Recovered              bool
}

// IncidentTransitions evaluates two consecutive five-minute windows. A quiet
// window alone is insufficient evidence of recovery.
func (s *Store) IncidentTransitions(ctx context.Context, now time.Time) ([]IncidentTransition, error) {
	const query = `
 WITH recent AS (
  SELECT model,json_extract(diagnostics,'$.protocol') AS protocol,json_extract(diagnostics,'$.code') AS code,
    status,json_extract(diagnostics,'$.completed') AS completed, created_at
  FROM request_logs WHERE created_at>=? AND created_at<? AND model<>''
 ), totals AS (
  SELECT model,protocol,sum(status BETWEEN 200 AND 299 AND completed IS 1 AND created_at>=?) AS successes
  FROM recent WHERE protocol IN ('responses','messages','chat','antigravity') GROUP BY model,protocol
 ), failures AS (
  SELECT model,protocol,code,sum(created_at<?) AS older,sum(created_at>=?) AS newer
  FROM recent WHERE code IN ('route_unavailable','stream_interrupted') AND protocol IN ('responses','messages','chat','antigravity')
  GROUP BY model,protocol,code
 )
 SELECT f.model,f.protocol,f.code,0 FROM failures f
 LEFT JOIN operational_incidents i ON i.model=f.model AND i.protocol=f.protocol AND i.cause=f.code
 WHERE f.older>=3 AND f.newer>=3 AND i.model IS NULL
 UNION ALL
 SELECT i.model,i.protocol,i.cause,1 FROM operational_incidents i
 JOIN totals t ON t.model=i.model AND t.protocol=i.protocol
 LEFT JOIN failures f ON f.model=i.model AND f.protocol=i.protocol AND f.code=i.cause
 WHERE t.successes>=3 AND f.model IS NULL
 ORDER BY 4 DESC,1,2,3 LIMIT 50`
	middle := now.Add(-5 * time.Minute).Unix()
	rows, err := s.DB.QueryContext(ctx, query, now.Add(-10*time.Minute).Unix(), now.Unix(), middle, middle, middle)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []IncidentTransition
	for rows.Next() {
		var v IncidentTransition
		if err = rows.Scan(&v.Model, &v.Protocol, &v.Cause, &v.Recovered); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

func (s *Store) RecordIncidentTransitions(ctx context.Context, changes []IncidentTransition) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, v := range changes {
		query := "INSERT OR IGNORE INTO operational_incidents(model,protocol,cause) VALUES(?,?,?)"
		if v.Recovered {
			query = "DELETE FROM operational_incidents WHERE model=? AND protocol=? AND cause=?"
		}
		if _, err = tx.ExecContext(ctx, query, v.Model, v.Protocol, v.Cause); err != nil {
			return err
		}
	}
	return tx.Commit()
}
