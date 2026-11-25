package backup

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"time"
)

type Health struct {
	LastSuccess int64  `json:"last_success,omitempty"`
	CheckedAt   int64  `json:"checked_at,omitempty"`
	CheckedID   string `json:"checked_id,omitempty"`
	Stale       bool   `json:"stale"`
}

func (s *Service) Health(ctx context.Context, now time.Time) (Health, error) {
	var h Health
	err := s.store.DB.QueryRowContext(ctx, `SELECT COALESCE(max(completed_at),0) FROM backup_records WHERE status IN ('completed','expired') AND sha256<>''`).Scan(&h.LastSuccess)
	if err != nil {
		return h, err
	}
	m, err := s.store.MaintenanceState(ctx, "backup_restore_check")
	if err != nil {
		return h, err
	}
	h.CheckedAt, h.CheckedID = m.LastSuccess, m.Revision
	c, err := s.Config(ctx)
	h.Stale = c.AutoEnabled && (h.LastSuccess == 0 || now.Unix()-h.LastSuccess >= int64((48*time.Hour)/time.Second))
	return h, err
}

// CheckRecord validates a retained archive on the data volume and records success.
func (s *Service) CheckRecord(ctx context.Context, id string) error {
	if !s.operations.TryLock() {
		return errors.New("已有备份操作正在执行")
	}
	defer s.operations.Unlock()
	ctx, cancel := context.WithTimeout(ctx, 30*time.Minute)
	defer cancel()
	record, resp, err := s.Download(ctx, id)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	dir, err := s.store.NewBackupWorkspace()
	if err != nil {
		return errors.New("创建备份临时目录失败")
	}
	defer os.RemoveAll(dir)
	path := filepath.Join(dir, "archive.tar.gz")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return errors.New("创建恢复预检文件失败")
	}
	hash := sha256.New()
	n, err := io.Copy(io.MultiWriter(f, hash), io.LimitReader(resp.Body, record.SizeBytes+1))
	closeErr := f.Close()
	if err != nil || closeErr != nil || n != record.SizeBytes || hex.EncodeToString(hash.Sum(nil)) != record.SHA256 {
		return errors.New("备份文件下载不完整或校验和不匹配")
	}
	if err := validateArchive(ctx, path, dir, s.key); err != nil {
		return err
	}
	_, err = s.store.DB.ExecContext(ctx, `INSERT INTO maintenance_state(name,last_success,revision) VALUES('backup_restore_check',?,?) ON CONFLICT(name) DO UPDATE SET last_success=excluded.last_success,revision=excluded.revision`, time.Now().Unix(), id)
	return err
}
