package server

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

func (a *API) importAccounts(w http.ResponseWriter, r *http.Request, id identity) {
	if a.accounts == nil {
		writeError(w, 503, "账号服务不可用")
		return
	}
	if !a.allowAttempt(r, "account-import", 3) {
		writeError(w, 429, "导入操作过于频繁，请稍后重试")
		return
	}
	if strings.Split(r.Header.Get("Content-Type"), ";")[0] != "application/json" {
		writeError(w, 415, "需要 application/json")
		return
	}
	var in struct {
		Provider string `json:"provider"`
		Verify   bool   `json:"verify"`
		Accounts []struct {
			Name string          `json:"name"`
			Auth json.RawMessage `json:"auth"`
		} `json:"accounts"`
	}
	d := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	d.DisallowUnknownFields()
	if d.Decode(&in) != nil || d.Decode(new(any)) != io.EOF || len(in.Accounts) < 1 || len(in.Accounts) > 20 {
		writeError(w, 400, "需要 1–20 个授权文件，JSON 总大小不得超过 1 MiB")
		return
	}
	if in.Provider != "openai" && in.Provider != "claude" && in.Provider != "antigravity" {
		writeError(w, 400, "供应商无效")
		return
	}
	type result struct {
		ID       int64  `json:"id,omitempty"`
		Name     string `json:"name"`
		Imported bool   `json:"imported"`
		Models   int    `json:"models"`
		Warning  string `json:"warning,omitempty"`
	}
	out := make([]result, len(in.Accounts))
	ctx, cancel := context.WithTimeout(r.Context(), 110*time.Second)
	defer cancel()
	for i, item := range in.Accounts {
		out[i].Name = item.Name
		account, err := a.accounts.ImportAuth(ctx, id.user.ID, item.Name, in.Provider, item.Auth)
		if err != nil {
			out[i].Warning = "导入失败：名称重复、授权字段无效或本地保存失败"
			continue
		}
		out[i].ID = account
		out[i].Imported = true
	}
	if in.Verify {
		slots := make(chan struct{}, 4)
		var wg sync.WaitGroup
		for i := range out {
			if !out[i].Imported {
				continue
			}
			index := i
			select {
			case <-ctx.Done():
				out[index].Warning = "账号已保存，目录尚未验证，请稍后验证并同步"
				continue
			case slots <- struct{}{}:
			}
			wg.Add(1)
			go func() {
				defer wg.Done()
				defer func() { <-slots }()
				task, stop := context.WithTimeout(ctx, 20*time.Second)
				defer stop()
				models, err := a.accounts.Verify(task, out[index].ID)
				if err != nil {
					out[index].Warning = "账号已加密保存，验证尚未成功；请在账号中查看状态并重试"
				} else {
					out[index].Models = len(models)
				}
			}()
		}
		wg.Wait()
	}
	imported := 0
	for _, r := range out {
		if r.Imported {
			imported++
		}
	}
	writeJSON(w, 200, map[string]any{"items": out, "imported": imported, "total": len(out)})
}
func (a *API) queryAccountQuota(w http.ResponseWriter, r *http.Request, id identity) {
	if a.accounts == nil {
		writeError(w, 503, "账号服务不可用")
		return
	}
	target, err := targetID(r)
	if err != nil {
		writeError(w, 400, err.Error())
		return
	}
	if !a.allowAttempt(r, "account-quota", 10) {
		writeError(w, 429, "查询过于频繁，请稍后重试")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	result, err := a.accounts.QueryOpenAIQuota(ctx, target)
	if err != nil {
		writeError(w, 502, err.Error())
		return
	}
	writeJSON(w, 200, result)
}
