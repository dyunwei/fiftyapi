package accounts

import (
	"context"
	"errors"
	"fiftyapi/internal/store"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRuntimeOutcomeOmitsProviderErrorContents(t *testing.T) {
	ctx := context.Background()
	db, err := store.Open(ctx, filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	service, err := New(db, make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	upstreamError := errors.New("Bearer secret-access-token; refresh_token=secret-refresh-token; raw-response")
	service.runtimeOutcome(ctx, 8, "model_sync", "模型同步", time.Now(), &upstreamError)
	result, err := db.RuntimeLogs(ctx, store.RuntimeLogFilter{}, 1, 20)
	if err != nil || result.Total != 1 {
		t.Fatal("missing failure event", err)
	}
	entry := result.Items[0]
	if entry.Level != "error" || entry.AccountID != 8 || strings.Contains(entry.Message, "secret") || strings.Contains(entry.Message, "raw-response") {
		t.Fatalf("unsafe event: %+v", entry)
	}
}
