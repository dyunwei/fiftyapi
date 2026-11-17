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
