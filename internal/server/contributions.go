package server

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"fiftyapi/internal/store"
)

func (a *API) listContributionLinks(w http.ResponseWriter, r *http.Request, id identity) {
	links, err := a.store.ContributionLinks(r.Context())
	if err != nil {
		writeError(w, 500, "读取贡献链接失败")
		return
	}
	writeJSON(w, 200, links)
}
func (a *API) createContributionLink(w http.ResponseWriter, r *http.Request, id identity) {
	var in struct {
		Provider string `json:"provider"`
		Note     string `json:"note"`
		Hours    int    `json:"hours"`
	}
	if !decode(w, r, &in) {
		return
	}
	if in.Hours == 0 {
		in.Hours = 24
	}
	in.Note = strings.TrimSpace(in.Note)
	if (in.Provider != "openai" && in.Provider != "claude" && in.Provider != "antigravity") || len(in.Note) > 200 || in.Hours < 1 || in.Hours > 168 {
		writeError(w, 400, "请选择供应商，有效期需为 1–168 小时，备注不超过 200 字节")
		return
	}
	token, err := randomToken()
	if err != nil {
		writeError(w, 500, "生成贡献链接失败")
		return
	}
	hash := sha256.Sum256([]byte(token))
	expires := time.Now().Add(time.Duration(in.Hours) * time.Hour)
	link, err := a.store.CreateContributionLink(r.Context(), id.user.ID, hash[:], in.Provider, in.Note, expires)
	if err != nil {
		writeError(w, 500, "保存贡献链接失败")
		return
	}
	// Keep the bearer token in the URL fragment, away from access logs and referrers.
	writeJSON(w, 201, map[string]any{"id": link, "fragment": "#contribute=" + token, "expires_at": expires.Unix()})
}
func (a *API) revokeContributionLink(w http.ResponseWriter, r *http.Request, id identity) {
	target, err := targetID(r)
	if err != nil {
		writeError(w, 400, err.Error())
		return
	}
	err = a.store.RevokeContributionLink(r.Context(), id.user.ID, target)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, 404, "贡献链接不存在")
		return
	}
	if err != nil {
		writeError(w, 500, "停用贡献链接失败")
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}
func (a *API) contributionLink(w http.ResponseWriter, r *http.Request, token string) (store.ContributionLink, bool) {
	if len(token) != 43 {
		writeError(w, 410, "链接已失效，请联系管理员获取新链接")
		return store.ContributionLink{}, false
	}
	hash := sha256.Sum256([]byte(token))
	link, err := a.store.ContributionLink(r.Context(), hash[:])
	if err != nil {
		writeError(w, 410, "链接已失效，请联系管理员获取新链接")
		return store.ContributionLink{}, false
	}
	return link, true
}
func (a *API) checkContributionLink(w http.ResponseWriter, r *http.Request) {
	if !a.allowAttempt(r, "contribution-check", 30) {
		writeError(w, 429, "请求过于频繁，请稍后重试")
		return
	}
	var in struct {
		Token string `json:"token"`
	}
	if !decode(w, r, &in) {
		return
	}
	if link, ok := a.contributionLink(w, r, in.Token); ok {
		writeJSON(w, 200, map[string]any{"provider": link.Provider, "expires_at": link.ExpiresAt})
	}
}
func (a *API) contributeAccounts(w http.ResponseWriter, r *http.Request) {
	if a.accounts == nil {
		writeError(w, 503, "账号服务不可用")
		return
	}
	if !a.allowAttempt(r, "contribution-import", 3) {
		writeError(w, 429, "导入操作过于频繁，请稍后重试")
		return
	}
	if strings.Split(r.Header.Get("Content-Type"), ";")[0] != "application/json" {
		writeError(w, 415, "需要 application/json")
		return
	}
	var in struct {
		Token    string `json:"token"`
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
	link, ok := a.contributionLink(w, r, in.Token)
	if !ok {
		return
	}
	type result struct {
		Name     string `json:"name"`
		Imported bool   `json:"imported"`
		Warning  string `json:"warning,omitempty"`
	}
	out := make([]result, len(in.Accounts))
	imported := 0
	for i, item := range in.Accounts {
		out[i].Name = item.Name
		_, err := a.accounts.ImportContributedAuth(r.Context(), link.ActorID, item.Name, link.Provider, item.Auth, link.ID)
		if err != nil {
			out[i].Warning = "未导入：授权格式无效、名称或授权重复，或链接已失效"
			continue
		}
		out[i].Imported = true
		imported++
	}
	writeJSON(w, 200, map[string]any{"items": out, "imported": imported, "total": len(out)})
}
