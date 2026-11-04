package backup

import (
	"context"
	"errors"
	"fmt"
	"time"
)

func DailyTime(c Config, now time.Time) time.Time {
	var hour, minute int
	fmt.Sscanf(c.DailyTime, "%d:%d", &hour, &minute)
	return time.Date(now.Year(), now.Month(), now.Day(), hour, minute, 0, 0, now.Location())
}
func (s *Service) NextRun(ctx context.Context, c Config, now time.Time) (*time.Time, error) {
	if !c.AutoEnabled {
		return nil, nil
	}
	var count int
	if err := s.store.DB.QueryRowContext(ctx, "SELECT count(*) FROM backup_records WHERE scheduled_day=?", now.Format("2006-01-02")).Scan(&count); err != nil {
		return nil, err
	}
	next := DailyTime(c, now)
	if count > 0 {
		next = DailyTime(c, now.AddDate(0, 0, 1))
	} else if next.Before(now) {
		next = now
	}
	return &next, nil
}
func (s *Service) StartBackground(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.started {
		return nil
	}
	if s.active {
		return ErrBusy
	}
	if s.closing {
		return errors.New("服务正在关闭")
	}
	if err := s.store.CleanupBackupWorkspaces(); err != nil {
		s.event(ctx, "warn", "workspace_cleanup", "上次中断的备份临时文件无法清理，请检查数据目录权限", 0)
	}
	result, err := s.store.DB.ExecContext(ctx, "UPDATE backup_records SET status='failed',error='服务重启导致备份中断，可重新创建备份',completed_at=? WHERE status='running'", time.Now().Unix())
	if err != nil {
		return err
	}
	if n, _ := result.RowsAffected(); n > 0 {
		s.event(ctx, "warn", "recovered", "已将服务中断的备份标记为失败", 0)
	}
	s.started = true
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for {
			if ctx.Err() != nil || s.ctx.Err() != nil {
				return
			}
			s.tick(s.ctx, time.Now())
			select {
			case <-ctx.Done():
				return
			case <-s.ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
	return nil
}
func (s *Service) tick(ctx context.Context, now time.Time) {
	c, err := s.Config(ctx)
	if err != nil {
		s.event(ctx, "error", "schedule_config", "自动备份无法读取存储配置，请检查原主密钥", 0)
	} else if c.AutoEnabled {
		next, err := s.NextRun(ctx, c, now)
		if err != nil {
			s.event(ctx, "error", "schedule_check", "自动备份无法读取执行记录", 0)
		} else if next != nil && !next.After(now) {
			if _, err = s.Start(0, "automatic", now.Format("2006-01-02")); err != nil && !errors.Is(err, ErrBusy) {
				s.event(ctx, "error", "schedule_start", "自动备份未能启动，请检查存储配置与数据库状态", 0)
			}
		}
	}
	s.cleanup(ctx, now)
}
func (s *Service) cleanup(ctx context.Context, now time.Time) {
	rows, err := s.store.DB.QueryContext(ctx, "SELECT id FROM backup_records WHERE status IN ('completed','failed') AND expires_at<=? ORDER BY expires_at LIMIT 20", now.Unix())
	if err != nil {
		s.event(ctx, "error", "cleanup", "无法读取过期备份记录", 0)
		return
	}
	var ids []string
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			break
		}
		ids = append(ids, id)
	}
	scanErr := rows.Err()
	rows.Close()
	if err != nil || scanErr != nil {
		s.event(ctx, "error", "cleanup", "无法读取过期备份记录", 0)
		return
	}
	task, cancel := context.WithTimeout(ctx, 25*time.Second)
	defer cancel()
	for _, id := range ids {
		if task.Err() != nil {
			return
		}
		if err = s.Delete(task, id, true); err != nil {
			s.event(ctx, "error", "cleanup", "过期备份删除失败（"+id+"），将自动重试；请检查原存储配置和删除权限", 0)
		}
	}
}
