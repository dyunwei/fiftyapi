package server

import (
	"fiftyapi/internal/store"
	"net/http"
)

func (a *API) userAccess(w http.ResponseWriter, r *http.Request, id identity) {
	target, err := targetID(r)
	if err != nil {
		writeError(w, 400, err.Error())
		return
	}
	if r.Method == http.MethodGet {
		out, err := a.store.UserAccess(r.Context(), target)
		if err != nil {
			writeError(w, 500, "读取用户访问限制失败")
			return
		}
		writeJSON(w, 200, out)
		return
	}
	var in store.UserAccess
	if !decode(w, r, &in) {
		return
	}
	if err = a.store.SetUserAccess(r.Context(), id.user.ID, target, in); err != nil {
		writeError(w, 400, err.Error())
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func (a *API) setUserGroup(w http.ResponseWriter, r *http.Request, id identity) {
	target, err := targetID(r)
	if err != nil {
		writeError(w, 400, err.Error())
		return
	}
	var in struct {
		GroupID *int64 `json:"group_id"`
	}
	if !decode(w, r, &in) {
		return
	}
	if in.GroupID == nil {
		writeError(w, 400, "请选择访问分组")
		return
	}
	if err := a.store.SetUserGroup(r.Context(), id.user.ID, target, *in.GroupID); err != nil {
		writeAccessError(w, err)
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}
