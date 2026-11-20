package server

import (
	"context"
	"log"
	"time"

	"fiftyapi/internal/mailer"
)

func (a *API) RunMaintenanceAlerts(ctx context.Context) {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()
	for ctx.Err() == nil {
		if err := a.sendMaintenanceAlerts(ctx); err != nil && ctx.Err() == nil {
			log.Print("维护告警检查或发送失败，将在下次检查时重试")
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (a *API) sendMaintenanceAlerts(ctx context.Context) error {
	settings, _, err := a.readEmailSettings(ctx)
	if err != nil {
		return err
	}
	if settings.AlertRecipient == "" {
		return nil
	}
	issues, err := a.store.UnsentMaintenanceIssues(ctx)
	if err != nil || len(issues) == 0 {
		return err
	}
	var counts mailer.MaintenanceCounts
	for _, issue := range issues {
		switch issue.Kind {
		case "reauth":
			counts.Reauth++
		case "settlement":
			counts.Settlement++
		case "backup":
			counts.Backup++
		}
	}
	if err := settings.smtp().SendMaintenanceAlert(ctx, settings.AlertRecipient, counts); err != nil {
		return err
	}
	save, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
	defer cancel()
	return a.store.MarkMaintenanceIssuesSent(save, issues)
}
