package store

import (
	"context"
	"time"
)

type ErrorStat struct {
	Status   int   `json:"status"`
	Requests int64 `json:"requests"`
}
type Dashboard struct {
	Errors          []ErrorStat  `json:"errors"`
	Keys            int64        `json:"keys"`
	EnabledKeys     int64        `json:"enabled_keys"`
	Accounts        int64        `json:"accounts"`
	EnabledAccounts int64        `json:"enabled_accounts"`
	Users           int64        `json:"users"`
	EnabledUsers    int64        `json:"enabled_users"`
	TodayRequests   int64        `json:"today_requests"`
	TotalRequests   int64        `json:"total_requests"`
	TodayTokens     int64        `json:"today_tokens"`
	TotalTokens     int64        `json:"total_tokens"`
	TodayAmount     int64        `json:"today_amount"`
	TotalAmount     int64        `json:"total_amount"`
	RPM             int64        `json:"rpm"`
	TPM             int64        `json:"tpm"`
	AverageMS       float64      `json:"average_ms"`
	ActiveUsers     int64        `json:"active_users"`
	Trend           []TrendPoint `json:"trend"`
	Distribution    []ModelStat  `json:"distribution"`
}
type TrendPoint struct {
	Time       int64 `json:"time"`
	Input      int64 `json:"input"`
	Output     int64 `json:"output"`
	Cached     int64 `json:"cached"`
	CacheWrite int64 `json:"cache_write"`
	Requests   int64 `json:"requests"`
}
type ModelStat struct {
	Model    string `json:"model"`
	Requests int64  `json:"requests"`
	Tokens   int64  `json:"tokens"`
	Amount   int64  `json:"amount"`
}

func (s *Store) Dashboard(ctx context.Context, user int64, admin bool, hours, bucket, offset int) (Dashboard, error) {
	out := Dashboard{Errors: []ErrorStat{}, Trend: []TrendPoint{}, Distribution: []ModelStat{}}
	now := time.Now().Unix()
	local := now + int64(offset)*60
	today := local/86400*86400 - int64(offset)*60
	keyWhere := " WHERE deleted_at IS NULL"
	keyArgs := []any{}
	where := " WHERE 1=1"
	args := []any{}
	if !admin {
		keyWhere += " AND user_id=?"
		keyArgs = append(keyArgs, user)
		where += " AND user_id=?"
		args = append(args, user)
	}
	if err := s.DB.QueryRowContext(ctx, "SELECT count(*),COALESCE(sum(enabled),0) FROM api_keys"+keyWhere, keyArgs...).Scan(&out.Keys, &out.EnabledKeys); err != nil {
		return out, err
	}
	if admin {
		if err := s.DB.QueryRowContext(ctx, "SELECT count(*),COALESCE(sum(enabled),0) FROM upstream_accounts").Scan(&out.Accounts, &out.EnabledAccounts); err != nil {
			return out, err
		}
		if err := s.DB.QueryRowContext(ctx, "SELECT count(*),COALESCE(sum(enabled),0) FROM users").Scan(&out.Users, &out.EnabledUsers); err != nil {
			return out, err
		}
	}
	tokens := "COALESCE(input_tokens,0)+COALESCE(output_tokens,0)+COALESCE(cached_tokens,0)+COALESCE(cache_write_tokens,0)"
	query := "SELECT count(*),COALESCE(sum(" + tokens + "),0),COALESCE(sum(amount),0),COALESCE(avg(duration_ms),0),count(DISTINCT user_id) FROM request_logs" + where
	if err := s.DB.QueryRowContext(ctx, query, args...).Scan(&out.TotalRequests, &out.TotalTokens, &out.TotalAmount, &out.AverageMS, &out.ActiveUsers); err != nil {
		return out, err
	}
	if err := s.DB.QueryRowContext(ctx, "SELECT count(*),COALESCE(sum("+tokens+"),0),COALESCE(sum(amount),0) FROM request_logs"+where+" AND created_at>=?", append(append([]any{}, args...), today)...).Scan(&out.TodayRequests, &out.TodayTokens, &out.TodayAmount); err != nil {
		return out, err
	}
	if err := s.DB.QueryRowContext(ctx, "SELECT count(*),COALESCE(sum("+tokens+"),0) FROM request_logs"+where+" AND created_at>=?", append(append([]any{}, args...), now-60)...).Scan(&out.RPM, &out.TPM); err != nil {
		return out, err
	}
	from := now - int64(hours)*3600
	rangeArgs := append(append([]any{}, args...), from)
	rows, err := s.DB.QueryContext(ctx, "SELECT (created_at/"+bucketSQL(bucket)+")*"+bucketSQL(bucket)+",COALESCE(sum(input_tokens),0),COALESCE(sum(output_tokens),0),COALESCE(sum(cached_tokens),0),COALESCE(sum(cache_write_tokens),0),count(*) FROM request_logs"+where+" AND created_at>=? GROUP BY 1 ORDER BY 1", rangeArgs...)
	if err != nil {
		return out, err
	}
	for rows.Next() {
		var p TrendPoint
		if err = rows.Scan(&p.Time, &p.Input, &p.Output, &p.Cached, &p.CacheWrite, &p.Requests); err != nil {
			rows.Close()
			return out, err
		}
		out.Trend = append(out.Trend, p)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return out, err
	}
	rows, err = s.DB.QueryContext(ctx, "SELECT model,count(*),COALESCE(sum("+tokens+"),0),COALESCE(sum(amount),0) FROM request_logs"+where+" AND created_at>=? GROUP BY model ORDER BY 3 DESC", rangeArgs...)
	if err != nil {
		return out, err
	}
	for rows.Next() {
		var m ModelStat
		if err = rows.Scan(&m.Model, &m.Requests, &m.Tokens, &m.Amount); err != nil {
			rows.Close()
			return out, err
		}
		out.Distribution = append(out.Distribution, m)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return out, err
	}
	rows, err = s.DB.QueryContext(ctx, "SELECT status,count(*) FROM request_logs"+where+" AND created_at>=? AND status>=400 GROUP BY status ORDER BY count(*) DESC,status", rangeArgs...)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var e ErrorStat
		if err = rows.Scan(&e.Status, &e.Requests); err != nil {
			return out, err
		}
		out.Errors = append(out.Errors, e)
	}
	return out, rows.Err()
}
func bucketSQL(bucket int) string {
	if bucket == 86400 {
		return "86400"
	}
	return "3600"
}
