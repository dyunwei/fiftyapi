package accounts

import (
	"context"
	"fiftyapi/internal/store"
	"sync"
	"time"
)

const catalogSyncInterval = 24 * time.Hour

// Maintain retries temporary failures and pending projects, refreshes expiring
// OAuth credentials through Access, and resynchronizes healthy catalogs once a day.
// No background task onboards accounts without an explicit OAuth consent step.
func (s *Service) Maintain(ctx context.Context) {
	rows, err := s.store.Accounts(ctx)
	if err != nil {
		s.store.RecordRuntime(ctx, store.RuntimeLog{Level: "error", Module: "accounts", Event: "maintenance", Message: "账号维护无法读取账号列表"})
		return
	}
	slots := make(chan struct{}, 4)
	var wg sync.WaitGroup
	for _, row := range rows {
		// A deliberately unverified import must not start contacting its provider
		// merely because the process restarted or a maintenance tick elapsed.
		if !row.Enabled || row.State == "reauth_required" || (row.State != "catalog_ready" && row.State != "ready" && row.ModelCount == 0) {
			continue
		}
		last, ok := s.checked.Load(row.ID)
		needVerify := !ok || row.State != "catalog_ready" || row.LastError != "" || time.Since(last.(time.Time)) >= catalogSyncInterval
		if !needVerify && row.Provider == "gateway" {
			continue
		}
		select {
		case <-ctx.Done():
			wg.Wait()
			return
		case slots <- struct{}{}:
		}
		wg.Add(1)
		go func(id int64, verify bool) {
			defer wg.Done()
			defer func() { <-slots }()
			task, cancel := context.WithTimeout(ctx, 12*time.Second)
			defer cancel()
			if verify {
				s.Verify(task, id)
				return
			}
			_, _ = s.Access(task, id)
		}(row.ID, needVerify)
	}
	wg.Wait()
}
