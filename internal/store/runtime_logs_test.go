package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func TestRuntimeLogsAggregationFiltersRetentionAndPersistence(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "runtime.db")
	s, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { s.Close() }()
	e := RuntimeLog{Level: "error", Module: "accounts", Event: "token_refresh", AccountID: 42, Message: "令牌刷新失败", DurationMS: 17}
	s.RecordRuntime(ctx, e)
	s.RecordRuntime(ctx, e)
	s.RecordRuntime(ctx, RuntimeLog{Level: "info", Module: "server", Event: "started", Message: "HTTP 服务已启动"})
	result, err := s.RuntimeLogs(ctx, RuntimeLogFilter{Level: "error", Module: "accounts", AccountID: 42, Since: time.Now().Add(-time.Minute).Unix(), Until: time.Now().Add(time.Minute).Unix()}, 1, 20)
	if err != nil || result.Total != 1 || len(result.Items) != 1 || result.Items[0].Occurrences != 2 || result.Items[0].DurationMS != 17 {
		t.Fatalf("aggregation/filter failed: %+v %v", result, err)
	}
	empty, err := s.RuntimeLogs(ctx, RuntimeLogFilter{AccountID: 99}, 1, 20)
	if err != nil || empty.Total != 0 || empty.Items == nil {
		t.Fatalf("empty result: %+v %v", empty, err)
	}
	first, err := s.RuntimeLogs(ctx, RuntimeLogFilter{}, 1, 1)
	if err != nil || first.Total != 2 || len(first.Items) != 1 {
		t.Fatalf("pagination: %+v %v", first, err)
	}
	// Expired entries are immediately hidden, then physically removed by cleanup.
	if _, err = s.DB.Exec("UPDATE runtime_logs SET last_at=? WHERE module='accounts'", time.Now().Add(-8*24*time.Hour).Unix()); err != nil {
		t.Fatal(err)
	}
	visible, err := s.RuntimeLogs(ctx, RuntimeLogFilter{}, 1, 20)
	if err != nil || visible.Total != 1 {
		t.Fatal("expired entry visible", err)
	}
	if err = s.CleanupRuntimeLogs(ctx); err != nil {
		t.Fatal(err)
	}
	var n int
	if err = s.DB.QueryRow("SELECT count(*) FROM runtime_logs").Scan(&n); err != nil || n != 1 {
		t.Fatal("cleanup failed", n, err)
	}
	s.Close()
	s, err = Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	saved, err := s.RuntimeLogs(ctx, RuntimeLogFilter{}, 1, 20)
	if err != nil || saved.Total != 1 || saved.Items[0].Event != "started" {
		t.Fatal("logs did not survive restart", err)
	}
}

func TestRuntimeLoggingAfterCancellation(t *testing.T) {
	s, err := Open(context.Background(), filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	s.RecordRuntime(ctx, RuntimeLog{Level: "info", Module: "server", Event: "stopped", Message: "HTTP 服务已关闭"})
	result, err := s.RuntimeLogs(context.Background(), RuntimeLogFilter{}, 1, 20)
	if err != nil || result.Total != 1 {
		t.Fatal("shutdown event lost", err)
	}
}
