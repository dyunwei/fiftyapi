package server

import (
	"context"
	"errors"
	"net/http"
	"time"

	"fiftyapi/internal/accounts"
	"fiftyapi/internal/store"
)

func (a *API) listProxies(w http.ResponseWriter, r *http.Request, id identity) {
	if a.accounts == nil {
		list, err := a.store.Proxies(r.Context())
		if err != nil {
			writeError(w, 500, "读取代理服务器失败")
			return
		}
		writeJSON(w, 200, list)
		return
	}
	list, err := a.accounts.Proxies(r.Context())
	if err != nil {
		writeError(w, 500, "读取代理服务器失败")
		return
	}
	writeJSON(w, 200, list)
}

func (a *API) createProxy(w http.ResponseWriter, r *http.Request, id identity) {
	if a.accounts == nil {
		writeError(w, 503, "账号服务不可用")
		return
	}
	var in accounts.ProxyInput
	if !decode(w, r, &in) {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	created, err := a.accounts.CreateProxy(ctx, id.user.ID, in)
	if err != nil {
		writeError(w, 400, err.Error())
		return
	}
	writeJSON(w, 201, created)
}

func (a *API) updateProxy(w http.ResponseWriter, r *http.Request, id identity) {
	if a.accounts == nil {
		writeError(w, 503, "账号服务不可用")
		return
	}
	target, err := targetID(r)
	if err != nil {
		writeError(w, 400, err.Error())
		return
	}
	var in accounts.ProxyInput
	if !decode(w, r, &in) {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	updated, err := a.accounts.UpdateProxy(ctx, id.user.ID, target, in)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, 404, "代理服务器不存在")
		return
	}
	if err != nil {
		writeError(w, 400, err.Error())
		return
	}
	writeJSON(w, 200, updated)
}

func (a *API) deleteProxy(w http.ResponseWriter, r *http.Request, id identity) {
	if a.accounts == nil {
		writeError(w, 503, "账号服务不可用")
		return
	}
	target, err := targetID(r)
	if err != nil {
		writeError(w, 400, err.Error())
		return
	}
	err = a.accounts.DeleteProxy(r.Context(), id.user.ID, target)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, 404, "代理服务器不存在")
		return
	}
	if errors.Is(err, store.ErrProxyInUse) {
		writeError(w, 400, err.Error())
		return
	}
	if err != nil {
		writeError(w, 500, "删除代理服务器失败")
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func (a *API) checkProxy(w http.ResponseWriter, r *http.Request, id identity) {
	if a.accounts == nil {
		writeError(w, 503, "账号服务不可用")
		return
	}
	target, err := targetID(r)
	if err != nil {
		writeError(w, 400, err.Error())
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	checked, err := a.accounts.CheckProxy(ctx, target)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, 404, "代理服务器不存在")
		return
	}
	if err != nil {
		writeError(w, 500, "检测代理服务器失败")
		return
	}
	writeJSON(w, 200, checked)
}

func (a *API) checkAllProxies(w http.ResponseWriter, r *http.Request, id identity) {
	if a.accounts == nil {
		writeError(w, 503, "账号服务不可用")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	list, err := a.accounts.CheckAllProxies(ctx)
	if err != nil {
		writeError(w, 500, "检测代理服务器失败")
		return
	}
	writeJSON(w, 200, list)
}
