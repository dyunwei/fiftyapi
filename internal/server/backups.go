package server

import (
	"context"
	"errors"
	"fiftyapi/internal/backup"
	"io"
	"net/http"
	"strconv"
	"time"
)

func (a *API) StartBackups(ctx context.Context) error { return a.backups.StartBackground(ctx) }
func (a *API) backupConfig(w http.ResponseWriter, r *http.Request, _ identity) {
	c, err := a.backups.Config(r.Context())
	if err != nil {
		writeError(w, 500, "无法读取备份配置，请检查原主密钥")
		return
	}
	next, err := a.backups.NextRun(r.Context(), c, time.Now())
	if err != nil {
		writeError(w, 500, "无法读取备份计划")
		return
	}
	access, secret := c.AccessKey != "", c.SecretKey != ""
	c.AccessKey = ""
	c.SecretKey = ""
	writeJSON(w, 200, map[string]any{"config": c, "access_key_set": access, "secret_key_set": secret, "editable": len(a.cfg.SettingsKey) == 32, "timezone": time.Now().Format("MST -07:00"), "next_run": next})
}
func (a *API) saveBackupConfig(w http.ResponseWriter, r *http.Request, id identity) {
	if len(a.cfg.SettingsKey) != 32 {
		writeError(w, 503, "备份配置加密密钥不可用")
		return
	}
	var in backup.Config
	if !decode(w, r, &in) {
		return
	}
	if err := a.backups.SaveConfig(r.Context(), id.user.ID, in); err != nil {
		writeError(w, 400, err.Error())
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}
func (a *API) testBackupConfig(w http.ResponseWriter, r *http.Request, _ identity) {
	if !a.allowAttempt(r, "backup-test", 3) {
		writeError(w, 429, "连接测试过于频繁，请稍后重试")
		return
	}
	var in backup.Config
	if !decode(w, r, &in) {
		return
	}
	if err := a.backups.Test(r.Context(), in); err != nil {
		writeError(w, 400, err.Error())
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}
func (a *API) createBackup(w http.ResponseWriter, r *http.Request, id identity) {
	var in struct {
		RetentionDays *int `json:"retention_days"`
	}
	if !decode(w, r, &in) {
		return
	}
	var retention []int
	if in.RetentionDays != nil {
		retention = append(retention, *in.RetentionDays)
	}
	record, err := a.backups.Start(id.user.ID, "manual", "", retention...)
	if err != nil {
		status := 400
		if errors.Is(err, backup.ErrBusy) {
			status = 409
		}
		writeError(w, status, err.Error())
		return
	}
	writeJSON(w, 202, record)
}
func (a *API) listBackups(w http.ResponseWriter, r *http.Request, _ identity) {
	page, size := 1, 20
	q := r.URL.Query()
	for k, v := range q {
		if (k != "page" && k != "page_size") || len(v) != 1 {
			writeError(w, 400, "分页参数无效")
			return
		}
	}
	for _, v := range []struct {
		key  string
		dest *int
	}{{"page", &page}, {"page_size", &size}} {
		if q.Has(v.key) {
			n, err := strconv.Atoi(q.Get(v.key))
			if err != nil {
				writeError(w, 400, "分页参数无效")
				return
			}
			*v.dest = n
		}
	}
	records, total, err := a.backups.List(r.Context(), page, size)
	if err != nil {
		writeError(w, 400, "无法读取备份记录或分页参数无效")
		return
	}
	writeJSON(w, 200, map[string]any{"items": records, "total": total, "page": page, "page_size": size})
}
func (a *API) downloadBackup(w http.ResponseWriter, r *http.Request, _ identity) {
	record, resp, err := a.backups.Download(r.Context(), r.PathValue("id"))
	if err != nil {
		status := 400
		if errors.Is(err, backup.ErrNotFound) {
			status = 404
		}
		writeError(w, status, err.Error())
		return
	}
	defer resp.Body.Close()
	w.Header().Set("Content-Type", "application/gzip")
	w.Header().Set("Content-Disposition", `attachment; filename="`+record.Filename+`"`)
	if resp.ContentLength >= 0 {
		w.Header().Set("Content-Length", strconv.FormatInt(resp.ContentLength, 10))
	}
	io.Copy(w, resp.Body)
}
func (a *API) deleteBackup(w http.ResponseWriter, r *http.Request, _ identity) {
	task, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	if err := a.backups.Delete(task, r.PathValue("id"), false); err != nil {
		status := 400
		if errors.Is(err, backup.ErrNotFound) {
			status = 404
		}
		if errors.Is(err, backup.ErrBusy) {
			status = 409
		}
		writeError(w, status, err.Error())
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}
