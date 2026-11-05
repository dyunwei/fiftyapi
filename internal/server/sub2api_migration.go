package server

import (
	"crypto/sha256"
	"encoding/json"
	"fiftyapi/internal/accounts"
	"fiftyapi/internal/store"
	"fmt"
	"io"
	"net/http"
	"strings"
	"unicode"
)

type sub2apiBundle struct {
	Type     string                    `json:"type"`
	Version  int                       `json:"version"`
	Source   string                    `json:"source"`
	Users    []store.MigrationUser     `json:"users"`
	Keys     []store.MigrationKey      `json:"api_keys"`
	Accounts []accounts.Sub2APIAccount `json:"accounts"`
	Proxies  []accounts.Sub2APIProxy   `json:"proxies"`
}

func (a *API) migrateSub2API(w http.ResponseWriter, r *http.Request, id identity) {
	if a.accounts == nil || len(a.cfg.SettingsKey) != 32 {
		writeError(w, 503, "账号服务或主密钥不可用")
		return
	}
	pending, _, err := a.store.Setting(r.Context(), "sub2api_onboarding_pending")
	if err != nil {
		writeError(w, 500, "读取迁移引导状态失败")
		return
	}
	if pending != "1" {
		writeError(w, 403, "sub2api 迁移仅在首次初始化向导中可用")
		return
	}
	if !a.allowAttempt(r, "sub2api-migration", 10) {
		writeError(w, 429, "迁移操作过于频繁，请稍后重试")
		return
	}
	if strings.Split(r.Header.Get("Content-Type"), ";")[0] != "application/json" {
		writeError(w, 415, "需要 application/json")
		return
	}
	var in struct {
		Data            sub2apiBundle `json:"data"`
		Preview         bool          `json:"preview"`
		AcceptKeyPolicy bool          `json:"accept_key_policy"`
	}
	d := json.NewDecoder(http.MaxBytesReader(w, r.Body, 32<<20))
	d.DisallowUnknownFields()
	if d.Decode(&in) != nil || d.Decode(new(any)) != io.EOF {
		writeError(w, 400, "迁移文件格式无效或超过 32 MiB")
		return
	}
	b := in.Data
	if b.Type != "fiftyapi-sub2api-migration" || b.Version != 1 || strings.TrimSpace(b.Source) == "" || len(b.Source) > 128 || strings.IndexFunc(b.Source, unicode.IsControl) >= 0 || len(b.Users) > 5000 || len(b.Keys) > 20000 || len(b.Accounts) > 1000 || len(b.Proxies) > 1000 || len(b.Users)+len(b.Keys)+len(b.Accounts) == 0 {
		writeError(w, 400, "请选择迁移工具生成的 v1 文件；最多 5000 用户、20000 Key、1000 账号和 1000 代理")
		return
	}
	canonical, err := json.Marshal(b)
	if err != nil {
		writeError(w, 400, "迁移文件格式无效或超过 32 MiB")
		return
	}
	digest := fmt.Sprintf("%x", sha256.Sum256(canonical))
	// Log only a whole-bundle checksum and counts, never credentials or identities.
	a.store.RecordRuntime(r.Context(), store.RuntimeLog{Level: "info", Module: "accounts", Event: "sub2api_bundle_received", Message: fmt.Sprintf("preview=%t bundle=%s users=%d keys=%d accounts=%d", in.Preview, digest, len(b.Users), len(b.Keys), len(b.Accounts))})
	// Validate proxy identities before committing any user records.
	seen := map[int64]bool{}
	for _, p := range b.Proxies {
		if p.ID <= 0 || seen[p.ID] {
			writeError(w, 400, "代理 ID 无效或重复")
			return
		}
		seen[p.ID] = true
	}
	items, err := a.store.ImportSub2APIUsers(r.Context(), id.user.ID, b.Source, b.Users, b.Keys, a.cfg.SettingsKey, in.Preview, in.AcceptKeyPolicy)
	if err != nil {
		writeError(w, 500, "用户和 Key 迁移失败，未保存本批用户和 Key")
		return
	}
	accountItems, err := a.accounts.ImportSub2API(r.Context(), id.user.ID, b.Accounts, b.Proxies, in.Preview)
	// User/key transactions may already have committed; always return their results.
	warning := ""
	if err != nil {
		warning = "账号迁移未完成，请重新上传；已保存的用户和 Key 不会重复创建"
	}
	items = append(items, accountItems...)
	imported, skipped, disabled := 0, 0, 0
	for _, item := range items {
		if item.Status == "imported" {
			imported++
			if !item.Enabled {
				disabled++
			}
		} else {
			skipped++
		}
	}
	writeJSON(w, 200, map[string]any{"preview": in.Preview, "items": items, "imported": imported, "skipped": skipped, "disabled": disabled, "warning": warning, "bundle_digest": digest})
}

// Onboarding is authenticated; initialization never exposes a public import API.
func (a *API) migrationOnboarding(w http.ResponseWriter, r *http.Request, id identity) {
	value, _, err := a.store.Setting(r.Context(), "sub2api_onboarding_pending")
	if err != nil {
		writeError(w, 500, "读取迁移引导状态失败")
		return
	}
	writeJSON(w, 200, map[string]bool{"pending": value == "1"})
}
func (a *API) completeMigrationOnboarding(w http.ResponseWriter, r *http.Request, id identity) {
	var in struct{}
	if !decode(w, r, &in) {
		return
	}
	if err := a.store.SaveSetting(r.Context(), id.user.ID, "sub2api_onboarding_pending", nil); err != nil {
		writeError(w, 500, "保存迁移引导状态失败")
		return
	}
	writeJSON(w, 200, map[string]bool{"complete": true})
}
