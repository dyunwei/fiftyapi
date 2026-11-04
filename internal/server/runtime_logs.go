package server

import (
	"fiftyapi/internal/store"
	"net/http"
	"strconv"
)

func (a *API) runtimeLogs(w http.ResponseWriter, r *http.Request, _ identity) {
	q := r.URL.Query()
	page, size := 1, 20
	allowed := map[string]bool{"page": true, "page_size": true, "level": true, "module": true, "account_id": true, "since": true, "until": true}
	for k, v := range q {
		if !allowed[k] || len(v) != 1 {
			writeError(w, 400, "日志筛选参数无效")
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
	f := store.RuntimeLogFilter{Level: q.Get("level"), Module: q.Get("module")}
	if page < 1 || page > 1000000 || size < 1 || size > 100 || (f.Level != "" && f.Level != "info" && f.Level != "warn" && f.Level != "error") || len(f.Module) > 64 {
		writeError(w, 400, "分页或筛选参数无效")
		return
	}
	for _, v := range []struct {
		key  string
		dest *int64
	}{{"account_id", &f.AccountID}, {"since", &f.Since}, {"until", &f.Until}} {
		if q.Has(v.key) {
			n, err := strconv.ParseInt(q.Get(v.key), 10, 64)
			if err != nil || n <= 0 {
				writeError(w, 400, "筛选参数无效："+v.key)
				return
			}
			*v.dest = n
		}
	}
	if f.Since > 0 && f.Until > 0 && f.Since > f.Until {
		writeError(w, 400, "开始时间不能晚于结束时间")
		return
	}
	result, err := a.store.RuntimeLogs(r.Context(), f, page, size)
	if err != nil {
		writeError(w, 500, "读取运行日志失败")
		return
	}
	writeJSON(w, 200, result)
}
