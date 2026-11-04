package server

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"time"

	"fiftyapi/internal/accounts"
	"fiftyapi/internal/gateway"
	"fiftyapi/internal/providers"
	"fiftyapi/internal/store"
)

func (a *API) AttachAccounts(service *accounts.Service) {
	a.accounts = service
	a.gateway = gateway.New(a.store, service)
}
func (a *API) listAccounts(w http.ResponseWriter, r *http.Request, id identity) {
	records, err := a.store.Accounts(r.Context())
	if err != nil {
		writeError(w, 500, "读取账号失败")
		return
	}
	views := []map[string]any{}
	for _, record := range records {
		view := map[string]any{"id": record.ID, "name": record.Name, "provider": record.Provider, "enabled": record.Enabled, "state": record.State, "last_error": record.LastError, "gateway_platform": record.Platform, "model_count": record.ModelCount, "models_synced_at": record.ModelsSyncedAt, "contribution_link_id": record.ContributionLinkID, "contributed_at": record.ContributedAt}
		if a.accounts != nil {
			if info, e := a.accounts.CredentialInfo(r.Context(), record.ID); e == nil {
				view["credential_info"] = info
			} else {
				view["credential_error"] = "凭据无法读取，请验证账号或重新授权"
			}
		}
		if policy, e := a.store.AccountPolicy(r.Context(), record.ID); e == nil {
			view["scheduling"] = policy
		}
		if health, e := a.store.AccountHealth(r.Context(), record.ID); e == nil {
			view["health"] = health
		}
		if a.gateway != nil {
			active, waiting := a.gateway.AccountLoad(record.ID)
			view["active"] = active
			view["waiting"] = waiting
		}
		views = append(views, view)
	}
	writeJSON(w, 200, views)
}
func (a *API) enableAccount(w http.ResponseWriter, r *http.Request, id identity) {
	target, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || target <= 0 {
		writeError(w, 400, "账号 ID 无效")
		return
	}
	var in struct {
		Enabled *bool `json:"enabled"`
	}
	if !decode(w, r, &in) {
		return
	}
	if in.Enabled == nil {
		writeError(w, 400, "缺少 enabled")
		return
	}
	err = a.store.EnableAccount(r.Context(), id.user.ID, target, *in.Enabled)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, 404, "账号不存在")
		return
	}
	if err != nil {
		writeError(w, 500, "更新账号失败")
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}
func (a *API) verifyAccount(w http.ResponseWriter, r *http.Request, id identity) {
	if a.accounts == nil {
		writeError(w, 503, "账号服务未配置")
		return
	}
	target, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || target <= 0 {
		writeError(w, 400, "账号 ID 无效")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Minute)
	defer cancel()
	models, err := a.accounts.Verify(ctx, target)
	if err != nil {
		writeError(w, 502, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"state": "catalog_ready", "models": models, "inference_verified": false})
}

func (a *API) startOAuth(w http.ResponseWriter, r *http.Request, id identity) {
	if a.accounts == nil {
		writeError(w, 503, "账号服务未配置")
		return
	}
	var in struct {
		Name      string `json:"name"`
		AccountID int64  `json:"account_id"`
	}
	if !decode(w, r, &in) {
		return
	}
	result, err := a.accounts.StartOAuth(r.Context(), id.user.ID, r.PathValue("provider"), in.Name, in.AccountID)
	if err != nil {
		writeError(w, 400, err.Error())
		return
	}
	writeJSON(w, 200, result)
}
func (a *API) completeOAuth(w http.ResponseWriter, r *http.Request, id identity) {
	if a.accounts == nil {
		writeError(w, 503, "账号服务未配置")
		return
	}
	var in struct {
		SessionID string `json:"session_id"`
		Result    string `json:"result"`
		Onboard   bool   `json:"onboard"`
	}
	if !decode(w, r, &in) {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Minute)
	defer cancel()
	account, warning, err := a.accounts.CompleteOAuth(ctx, id.user.ID, in.SessionID, in.Result, in.Onboard)
	if err != nil {
		writeError(w, 400, err.Error())
		return
	}
	writeJSON(w, 201, map[string]any{"id": account, "warning": warning})
}

func (a *API) accountConfig(w http.ResponseWriter, r *http.Request, id identity) {
	if a.accounts == nil {
		writeError(w, 503, "账号服务不可用")
		return
	}
	target, err := targetID(r)
	if err != nil {
		writeError(w, 400, err.Error())
		return
	}
	cfg, err := a.accounts.RuntimeConfig(r.Context(), target)
	if err != nil {
		writeError(w, 404, "读取账号配置失败")
		return
	}
	writeJSON(w, 200, cfg)
}
func (a *API) setAccountConfig(w http.ResponseWriter, r *http.Request, id identity) {
	if a.accounts == nil {
		writeError(w, 503, "账号服务不可用")
		return
	}
	target, err := targetID(r)
	if err != nil {
		writeError(w, 400, err.Error())
		return
	}
	var in accounts.RuntimeConfig
	if !decode(w, r, &in) {
		return
	}
	if err = a.accounts.UpdateRuntime(r.Context(), id.user.ID, target, in); err != nil {
		writeError(w, 400, err.Error())
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true, "needs_verify": true})
}

func (a *API) createGatewayAccount(w http.ResponseWriter, r *http.Request, id identity) {
	if a.accounts == nil {
		writeError(w, 503, "账号服务不可用")
		return
	}
	var in struct {
		Name string `json:"name"`
		accounts.RuntimeConfig
	}
	if !decode(w, r, &in) {
		return
	}
	cfg := providers.Config{InferencePolicy: in.InferencePolicy, Provider: "gateway", Gateway: in.Gateway, ProxyURL: in.ProxyURL, MaxConcurrency: in.MaxConcurrency, TimeoutSeconds: in.TimeoutSeconds, UserAgent: in.UserAgent, GatewayHeaders: in.GatewayHeaders, ModelsHeaders: in.ModelsHeaders}
	account, err := a.accounts.Import(r.Context(), id.user.ID, in.Name, accounts.Credential{Config: cfg, APIKey: in.APIKey})
	if err != nil {
		writeError(w, 400, "无法创建网关账号，请检查名称、API Key、协议及地址配置："+err.Error())
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Minute)
	defer cancel()
	names, err := a.accounts.Verify(ctx, account)
	warning := ""
	if cfg.Gateway != nil && cfg.Gateway.Discovery == "manual" {
		warning = "手工模型已保存，尚未验证上游连通性和调用权限"
	}
	if err != nil {
		warning = "账号已保存，请检查并重新验证：" + err.Error()
	}
	writeJSON(w, 201, map[string]any{"id": account, "models": names, "warning": warning})
}

func (a *API) accountModels(w http.ResponseWriter, r *http.Request, id identity) {
	target, err := targetID(r)
	if err != nil {
		writeError(w, 400, err.Error())
		return
	}
	page, size := 1, 20
	for _, v := range []struct {
		name   string
		target *int
		max    int
	}{{"page", &page, 1000000}, {"page_size", &size, 100}} {
		if raw, ok := r.URL.Query()[v.name]; ok {
			if len(raw) != 1 {
				writeError(w, 400, "分页参数无效")
				return
			}
			n, e := strconv.Atoi(raw[0])
			if e != nil || n < 1 || n > v.max {
				writeError(w, 400, "分页参数无效")
				return
			}
			*v.target = n
		}
	}
	search := r.URL.Query().Get("search")
	if len(search) > 256 {
		writeError(w, 400, "搜索条件过长")
		return
	}
	result, err := a.store.AccountModelsPage(r.Context(), target, page, size, search)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, 404, "账号不存在")
		return
	}
	if err != nil {
		writeError(w, 500, "读取账号模型失败")
		return
	}
	writeJSON(w, 200, result)
}
