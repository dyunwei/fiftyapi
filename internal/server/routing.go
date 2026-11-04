package server

import (
	"errors"
	"fiftyapi/internal/store"
	"net/http"
)

func (a *API) accountRouting(w http.ResponseWriter, r *http.Request, id identity) {
	target, err := targetID(r)
	if err != nil {
		writeError(w, 400, err.Error())
		return
	}
	if _, err = a.store.Account(r.Context(), target); err != nil {
		writeError(w, 404, "账号不存在")
		return
	}
	if a.accounts != nil {
		_, _ = a.accounts.CredentialInfo(r.Context(), target)
	}
	policy, err := a.store.AccountPolicy(r.Context(), target)
	if err != nil {
		writeError(w, 500, "读取调度配置失败")
		return
	}
	health, err := a.store.AccountHealth(r.Context(), target)
	if err != nil {
		writeError(w, 500, "读取账号健康信息失败")
		return
	}
	active, waiting := 0, 0
	if a.gateway != nil {
		active, waiting = a.gateway.AccountLoad(target)
	}
	writeJSON(w, 200, map[string]any{"policy": policy, "health": health, "active": active, "waiting": waiting})
}
func (a *API) setAccountRouting(w http.ResponseWriter, r *http.Request, id identity) {
	target, err := targetID(r)
	if err != nil {
		writeError(w, 400, err.Error())
		return
	}
	if _, err = a.store.Account(r.Context(), target); err != nil {
		writeError(w, 404, "账号不存在")
		return
	}
	var p store.AccountPolicy
	if !decode(w, r, &p) {
		return
	}
	if err = a.store.SetAccountPolicy(r.Context(), id.user.ID, target, p); err != nil {
		writeError(w, 400, "调度参数无效或保存失败")
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}
func (a *API) resetAccountRouting(w http.ResponseWriter, r *http.Request, id identity) {
	target, err := targetID(r)
	if err != nil {
		writeError(w, 400, err.Error())
		return
	}
	if _, err = a.store.Account(r.Context(), target); err != nil {
		writeError(w, 404, "账号不存在")
		return
	}
	if err = a.store.ResetAccountHealth(r.Context(), id.user.ID, target); err != nil {
		writeError(w, 500, "恢复调度失败")
		return
	}
	if a.gateway != nil {
		a.gateway.ResetAccountCooldown(target)
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}
func (a *API) requestAttempts(w http.ResponseWriter, r *http.Request, id identity) {
	target := r.PathValue("id")
	if len(target) > 128 {
		writeError(w, 400, "请求 ID 无效")
		return
	}
	items, err := a.store.RequestAttempts(r.Context(), id.user.ID, id.user.Role == "admin", target)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, 404, "请求不存在")
		return
	}
	if err != nil {
		writeError(w, 500, "读取路由记录失败")
		return
	}
	writeJSON(w, 200, items)
}
func (a *API) renameAccount(w http.ResponseWriter, r *http.Request, id identity) {
	target, err := targetID(r)
	if err != nil {
		writeError(w, 400, err.Error())
		return
	}
	var in struct {
		Name string `json:"name"`
	}
	if !decode(w, r, &in) {
		return
	}
	if err = a.store.RenameAccount(r.Context(), id.user.ID, target, in.Name); err != nil {
		writeError(w, 400, "名称为空、重复或账号不存在")
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}
func (a *API) deleteAccount(w http.ResponseWriter, r *http.Request, id identity) {
	target, err := targetID(r)
	if err != nil {
		writeError(w, 400, err.Error())
		return
	}
	if err = a.store.DeleteAccount(r.Context(), id.user.ID, target); err != nil {
		writeError(w, 400, "删除账号失败或账号不存在")
		return
	}
	if a.gateway != nil {
		a.gateway.ResetAccountCooldown(target)
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}
