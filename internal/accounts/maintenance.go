package accounts

import (
	"context"
	"fiftyapi/internal/store"
	"sync"
	"time"
)

// Maintain retries temporary failures and pending projects, refreshes expiring
// credentials through Verify, and resynchronizes catalogs every five minutes.
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
		if last, ok := s.checked.Load(row.ID); ok && row.State == "catalog_ready" && time.Since(last.(time.Time)) < 5*time.Minute {
			continue
		}
		select {
		case <-ctx.Done():
			wg.Wait()
			return
		case slots <- struct{}{}:
		}
		wg.Add(1)
		go func(id int64) {
			defer wg.Done()
			defer func() { <-slots }()
			task, cancel := context.WithTimeout(ctx, 12*time.Second)
			defer cancel()
			s.Verify(task, id)
		}(row.ID)
	}
	wg.Wait()
}
