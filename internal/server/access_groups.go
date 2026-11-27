package server

import (
	"encoding/json"
	"errors"
	"fiftyapi/internal/store"
	"net/http"
)

func (a *API) accessGroups(w http.ResponseWriter, r *http.Request, id identity) {
	ctx := r.Context()
	if r.Method == http.MethodGet {
		groups, err := a.store.AccessGroups(ctx)
		if err != nil {
			writeError(w, 500, "读取访问分组失败")
			return
		}
		writeJSON(w, 200, groups)
		return
	}
	var target int64
	if r.Method != http.MethodPost {
		var err error
		target, err = targetID(r)
		if err != nil {
			writeError(w, 400, err.Error())
			return
		}
	}
	if r.Method == http.MethodDelete {
		if err := a.store.DeleteAccessGroup(ctx, id.user.ID, target); err != nil {
			writeAccessError(w, err)
			return
		}
		writeJSON(w, 200, map[string]bool{"ok": true})
		return
	}
	var in struct {
		Name        string          `json:"name"`
		Models      json.RawMessage `json:"models"`
		Concurrency *int            `json:"concurrency"`
	}
	if !decode(w, r, &in) {
		return
	}
	var policy *store.AccessGroupPolicy
	if in.Models != nil || in.Concurrency != nil {
		policy = &store.AccessGroupPolicy{}
		if in.Concurrency == nil || in.Models == nil || json.Unmarshal(in.Models, &policy.Models) != nil {
			writeError(w, 400, "分组访问策略无效")
			return
		}
		policy.Concurrency = *in.Concurrency
	}
	group, err := a.store.SetAccessGroup(ctx, id.user.ID, target, in.Name, policy)
	if err != nil {
		writeAccessError(w, err)
		return
	}
	status := 200
	if r.Method == http.MethodPost {
		status = 201
	}
	writeJSON(w, status, group)
}

func (a *API) accountAccess(w http.ResponseWriter, r *http.Request, id identity) {
	target, err := targetID(r)
	if err != nil {
		writeError(w, 400, err.Error())
		return
	}
	if r.Method == http.MethodGet {
		access, err := a.store.AccountAccess(r.Context(), target)
		if err != nil {
			if errors.Is(err, store.ErrNotFound) {
				writeAccessError(w, err)
			} else {
				writeError(w, 500, "读取上游账号访问范围失败")
			}
			return
		}
		writeJSON(w, 200, access)
		return
	}
	var in store.AccountAccess
	if !decode(w, r, &in) {
		return
	}
	if err := a.store.SetAccountAccess(r.Context(), id.user.ID, target, in); err != nil {
		writeAccessError(w, err)
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func writeAccessError(w http.ResponseWriter, err error) {
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, 404, err.Error())
		return
	}
	writeError(w, 400, err.Error())
}
