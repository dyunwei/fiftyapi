package server

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/url"
	"strings"
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
	incidentErr := a.sendOperationalIncidents(ctx, settings)
	health, healthErr := a.backups.Health(ctx, time.Now())
	issues, err := a.store.UnsentMaintenanceIssues(ctx, healthErr == nil && health.Stale)
	if err != nil || len(issues) == 0 {
		return errors.Join(err, incidentErr)
	}
	var counts mailer.MaintenanceCounts
	for _, issue := range issues {
		switch issue.Kind {
		case "reauth":
			counts.Reauth++
		case "settlement":
			counts.Settlement++
		case "backup_stale":
			counts.BackupStale++
		case "backup":
			counts.Backup++
		}
	}
	if err := settings.smtp().SendMaintenanceAlert(ctx, settings.AlertRecipient, counts); err != nil {
		return err
	}
	save, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
	defer cancel()
	return errors.Join(incidentErr, a.store.MarkMaintenanceIssuesSent(save, issues))
}

func (a *API) sendOperationalIncidents(ctx context.Context, settings emailSettings) error {
	changes, err := a.store.IncidentTransitions(ctx, time.Now())
	if err != nil || len(changes) == 0 {
		return err
	}
	var body strings.Builder
	for _, v := range changes {
		state := "故障 / Incident"
		if v.Recovered {
			state = "已恢复 / Recovered"
		}
		cause := "上游断流 / Upstream stream interrupted"
		if v.Cause == "route_unavailable" {
			cause = "路由不可用 / Route unavailable"
		}
		fmt.Fprintf(&body, "%s · %q · %s · %s\n", state, v.Model, v.Protocol, cause)
		if validPublicURL(settings.PublicURL) {
			query := url.Values{"model": {v.Model}, "protocol": {v.Protocol}}
			fmt.Fprintf(&body, "%s/#models?%s\n", strings.TrimRight(settings.PublicURL, "/"), query.Encode())
		}
	}
	if err = settings.smtp().SendOperationalAlert(ctx, settings.AlertRecipient, body.String()); err != nil {
		return err
	}
	save, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
	defer cancel()
	return a.store.RecordIncidentTransitions(save, changes)
}
