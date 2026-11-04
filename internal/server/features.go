package server

import (
	"errors"
	"fiftyapi/internal/store"
	"golang.org/x/crypto/bcrypt"
	"net/http"
	"strconv"
	"strings"
)

func targetID(r *http.Request) (int64, error) {
	n, e := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if e != nil || n <= 0 {
		return 0, errors.New("ID 无效")
	}
	return n, nil
}
func (a *API) changePassword(w http.ResponseWriter, r *http.Request, id identity) {
	var in struct {
		OldPassword string `json:"old_password"`
		Password    string `json:"password"`
	}
	if !decode(w, r, &in) {
		return
	}
	if !validPassword(in.Password, id.user.Role) || len(in.OldPassword) > 72 {
		writeError(w, 400, "密码需为 8–72 字节")
		return
	}
	if bcrypt.CompareHashAndPassword([]byte(id.user.PasswordHash), []byte(in.OldPassword)) != nil {
		writeError(w, 403, "原密码错误")
		return
	}
	a.savePassword(w, r, id, id.user.ID, in.Password, true)
}
func (a *API) resetPassword(w http.ResponseWriter, r *http.Request, id identity) {
	target, err := targetID(r)
	if err != nil {
		writeError(w, 400, err.Error())
		return
	}
	var in struct {
		Password string `json:"password"`
	}
	if !decode(w, r, &in) {
		return
	}
	if len(in.Password) < 8 || len(in.Password) > 72 {
		writeError(w, 400, "密码需为 8–72 字节")
		return
	}
	a.savePassword(w, r, id, target, in.Password, false)
}
func (a *API) savePassword(w http.ResponseWriter, r *http.Request, id identity, target int64, password string, own bool) {
	hash, err := bcrypt.GenerateFromPassword([]byte(password), 12)
	if err == nil {
		err = a.store.ChangePassword(r.Context(), id.user.ID, target, string(hash), own)
	}
	if err != nil {
		writeError(w, 400, "密码修改失败")
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true, "sessions_revoked": true})
}
func (a *API) quota(w http.ResponseWriter, r *http.Request, id identity) {
	v, err := a.store.Quota(r.Context(), id.user.ID)
	if err != nil {
		writeError(w, 500, "读取额度失败")
		return
	}
	writeJSON(w, 200, v)
}
func (a *API) dashboard(w http.ResponseWriter, r *http.Request, id identity) {
	hours, _ := strconv.Atoi(r.URL.Query().Get("hours"))
	if hours != 24 && hours != 168 && hours != 720 {
		hours = 24
	}
	bucket := 3600
	if r.URL.Query().Get("granularity") == "day" {
		bucket = 86400
	}
	offset, _ := strconv.Atoi(r.URL.Query().Get("tz_offset"))
	if offset < -720 || offset > 840 {
		offset = 0
	}
	data, err := a.store.Dashboard(r.Context(), id.user.ID, id.user.Role == "admin", hours, bucket, offset)
	if err != nil {
		writeError(w, 500, "读取统计失败")
		return
	}
	writeJSON(w, 200, data)
}
func (a *API) setLimits(w http.ResponseWriter, r *http.Request, id identity) {
	target, err := targetID(r)
	if err != nil {
		writeError(w, 400, err.Error())
		return
	}
	var in struct {
		Limit5 int64 `json:"limit_5h_microusd"`
		Limit7 int64 `json:"limit_7d_microusd"`
	}
	if !decode(w, r, &in) {
		return
	}
	if err = a.store.SetLimits(r.Context(), id.user.ID, target, in.Limit5, in.Limit7); err != nil {
		writeError(w, 400, "更新额度失败")
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}
func (a *API) resetQuota(w http.ResponseWriter, r *http.Request, id identity) {
	target, err := targetID(r)
	if err != nil {
		writeError(w, 400, err.Error())
		return
	}
	var in struct {
		Kind string `json:"kind"`
	}
	if !decode(w, r, &in) {
		return
	}
	if err = a.store.ResetQuota(r.Context(), id.user.ID, target, in.Kind); err != nil {
		writeError(w, 400, "重置额度失败")
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}
func (a *API) keys(w http.ResponseWriter, r *http.Request, id identity) {
	keys, err := a.store.APIKeys(r.Context(), id.user.ID)
	if err != nil {
		writeError(w, 500, "读取密钥失败")
		return
	}
	writeJSON(w, 200, keys)
}
func (a *API) createKey(w http.ResponseWriter, r *http.Request, id identity) {
	var in struct {
		Name string `json:"name"`
	}
	if !decode(w, r, &in) {
		return
	}
	key, token, err := a.store.CreateAPIKey(r.Context(), id.user.ID, in.Name, a.cfg.SettingsKey)
	if err != nil {
		writeError(w, 400, "创建密钥失败")
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, 201, map[string]any{"key": key, "token": token})
}
func (a *API) revokeKey(w http.ResponseWriter, r *http.Request, id identity) {
	target, err := targetID(r)
	if err != nil {
		writeError(w, 400, err.Error())
		return
	}
	if err = a.store.RevokeAPIKey(r.Context(), id.user.ID, target); err != nil {
		writeError(w, 404, "密钥不存在")
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}
func (a *API) deleteKey(w http.ResponseWriter, r *http.Request, id identity) {
	target, err := targetID(r)
	if err != nil {
		writeError(w, 400, err.Error())
		return
	}
	if err = a.store.DeleteAPIKey(r.Context(), id.user.ID, target); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, 404, "密钥不存在")
			return
		}
		writeError(w, 500, "删除密钥失败")
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}
func (a *API) models(w http.ResponseWriter, r *http.Request, id identity) {
	q := r.URL.Query()
	if q.Has("page") || q.Has("page_size") || q.Has("q") || q.Has("account_id") || q.Has("protocol") || q.Has("status") {
		pageText, sizeText := q.Get("page"), q.Get("page_size")
		if !q.Has("page") {
			pageText = "1"
		}
		if !q.Has("page_size") {
			sizeText = "10"
		}
		page, e1 := strconv.Atoi(pageText)
		size, e2 := strconv.Atoi(sizeText)
		if e1 != nil || e2 != nil || page < 1 || page > 1000000 || size < 1 || size > 100 || len(r.URL.Query().Get("q")) > 200 {
			writeError(w, 400, "分页参数无效，page_size 需为 1–100")
			return
		}
		filter := store.ModelFilter{Protocol: q.Get("protocol"), Status: q.Get("status")}
		if filter.Protocol != "" && filter.Protocol != "chat" && filter.Protocol != "messages" && filter.Protocol != "responses" {
			writeError(w, 400, "协议筛选无效")
			return
		}
		switch filter.Status {
		case "", "disabled", "unavailable", "unpriced", "priced":
		default:
			writeError(w, 400, "模型状态筛选无效")
			return
		}
		if q.Has("account_id") {
			if !strings.HasPrefix(r.URL.Path, "/api/admin/") {
				writeError(w, 403, "仅管理员可按上游账号筛选")
				return
			}
			account, err := strconv.ParseInt(q.Get("account_id"), 10, 64)
			if err != nil || account <= 0 {
				writeError(w, 400, "上游账号筛选无效")
				return
			}
			filter.AccountID = account
		}
		result, err := a.store.ModelPage(r.Context(), page, size, r.URL.Query().Get("q"), !strings.HasPrefix(r.URL.Path, "/api/admin/"), filter)
		if err != nil {
			writeError(w, 500, "读取模型失败")
			return
		}
		writeJSON(w, 200, result)
		return
	}
	models, err := a.store.Models(r.Context())
	if err != nil {
		writeError(w, 500, "读取模型失败")
		return
	}
	if !strings.HasPrefix(r.URL.Path, "/api/admin/") {
		out := []store.Model{}
		for _, m := range models {
			if m.Enabled && m.Price != nil && m.Available {
				out = append(out, m)
			}
		}
		models = out
	}
	writeJSON(w, 200, models)
}
func (a *API) setModel(w http.ResponseWriter, r *http.Request, id identity) {
	var in struct {
		ID      string       `json:"id"`
		Enabled bool         `json:"enabled"`
		Price   *store.Price `json:"price"`
	}
	if !decode(w, r, &in) {
		return
	}
	if err := a.store.SetModel(r.Context(), id.user.ID, in.ID, in.Enabled, in.Price); err != nil {
		writeError(w, 400, "更新模型失败")
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}
func (a *API) logs(w http.ResponseWriter, r *http.Request, id identity) {
	q := r.URL.Query()
	if q.Has("page") || q.Has("page_size") || q.Has("settlement") || q.Has("until") {
		pageText, sizeText := q.Get("page"), q.Get("page_size")
		if !q.Has("page") {
			pageText = "1"
		}
		if !q.Has("page_size") {
			sizeText = "10"
		}
		page, e1 := strconv.Atoi(pageText)
		size, e2 := strconv.Atoi(sizeText)
		status, since, until := 0, int64(0), int64(0)
		var e3, e4, e5 error
		if q.Get("status") != "" {
			status, e3 = strconv.Atoi(q.Get("status"))
		}
		if q.Get("since") != "" {
			since, e4 = strconv.ParseInt(q.Get("since"), 10, 64)
		}
		if q.Has("until") {
			until, e5 = strconv.ParseInt(q.Get("until"), 10, 64)
		}

		settlement := q.Get("settlement")
		admin := strings.HasPrefix(r.URL.Path, "/api/admin/")
		if !admin && settlement != "" {
			writeError(w, 403, "结算异常筛选仅供管理员使用")
			return
		}
		if e1 != nil || e2 != nil || e3 != nil || e4 != nil || e5 != nil || until < 0 || (until > 0 && since > until) || page < 1 || page > 1000000 || size < 1 || size > 100 || status < 0 || status > 599 || since < 0 || len(q.Get("request_id")) > 100 || len(q.Get("model")) > 255 || (settlement != "" && settlement != "pending" && settlement != "settled" && settlement != "inflight") {
			writeError(w, 400, "日志分页或筛选参数无效")
			return
		}
		result, err := a.store.LogListPage(r.Context(), store.LogFilter{User: id.user.ID, Admin: admin, Model: q.Get("model"), RequestID: q.Get("request_id"), Status: status, Since: since, Until: until, Settlement: settlement}, page, size)
		if err != nil {
			writeError(w, 500, "读取日志失败")
			return
		}
		writeJSON(w, 200, result)
		return
	}
	before, _ := strconv.ParseInt(r.URL.Query().Get("before"), 10, 64)
	status, _ := strconv.Atoi(r.URL.Query().Get("status"))
	since, _ := strconv.ParseInt(r.URL.Query().Get("since"), 10, 64)
	cursor := r.URL.Query().Get("before_id")
	if len(cursor) > 100 {
		writeError(w, 400, "日志游标无效")
		return
	}
	logs, err := a.store.LogsPage(r.Context(), id.user.ID, strings.HasPrefix(r.URL.Path, "/api/admin/"), before, r.URL.Query().Get("model"), status, cursor, since, r.URL.Query().Get("request_id"))
	if err != nil {
		writeError(w, 500, "读取日志失败")
		return
	}
	writeJSON(w, 200, logs)
}
func (a *API) settlementDetails(w http.ResponseWriter, r *http.Request, id identity) {
	c, err := a.store.Charge(r.Context(), r.PathValue("id"))
	if err != nil {
		writeError(w, 404, "结算记录不存在")
		return
	}
	p, priceErr := a.store.PriceSnapshot(r.Context(), c.ID)
	writeJSON(w, 200, map[string]any{"charge": c, "price": p, "price_valid": priceErr == nil})
}
func (a *API) retrySettlement(w http.ResponseWriter, r *http.Request, id identity) {
	if err := a.store.RetrySettlement(r.Context(), id.user.ID, r.PathValue("id")); err != nil {
		writeError(w, 400, "结算未完成，请查看异常详情；仍可人工核算")
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}
func (a *API) auditLogs(w http.ResponseWriter, r *http.Request, id identity) {
	if len(r.URL.Query()) > 0 {
		q := r.URL.Query()
		page, size := 1, 10
		var err error
		if q.Has("page") {
			page, err = strconv.Atoi(q.Get("page"))
			if err != nil {
				writeError(w, 400, "分页参数无效")
				return
			}
		}
		if q.Has("page_size") {
			size, err = strconv.Atoi(q.Get("page_size"))
			if err != nil {
				writeError(w, 400, "分页参数无效")
				return
			}
		}
		f := store.AuditFilter{Action: q.Get("action"), Details: q.Get("details")}
		if page < 1 || page > 1000000 || size < 1 || size > 100 || len(f.Action) > 200 || len(f.Details) > 200 {
			writeError(w, 400, "分页或筛选参数无效")
			return
		}
		for _, v := range []struct {
			key  string
			dest *int64
		}{{"actor_id", &f.ActorID}, {"target_id", &f.TargetID}, {"since", &f.Since}, {"until", &f.Until}} {
			if q.Has(v.key) {
				*v.dest, err = strconv.ParseInt(q.Get(v.key), 10, 64)
				if err != nil || *v.dest <= 0 {
					writeError(w, 400, "筛选参数无效："+v.key)
					return
				}
			}
		}
		if f.Since > 0 && f.Until > 0 && f.Since > f.Until {
			writeError(w, 400, "开始时间不能晚于结束时间")
			return
		}
		result, err := a.store.AuditListPage(r.Context(), f, page, size)
		if err != nil {
			writeError(w, 500, "读取操作日志失败")
			return
		}
		writeJSON(w, 200, result)
		return
	}

	logs, err := a.store.AuditLogs(r.Context())
	if err != nil {
		writeError(w, 500, "读取操作日志失败")
		return
	}
	writeJSON(w, 200, logs)
}
func (a *API) pendingCharges(w http.ResponseWriter, r *http.Request, id identity) {
	v, err := a.store.PendingCharges(r.Context())
	if err != nil {
		writeError(w, 500, "读取待核算请求失败")
		return
	}
	writeJSON(w, 200, v)
}
func (a *API) manualSettle(w http.ResponseWriter, r *http.Request, id identity) {
	var in struct {
		Amount *int64 `json:"amount"`
		Reason string `json:"reason"`
	}
	if !decode(w, r, &in) {
		return
	}
	if in.Amount == nil {
		writeError(w, 400, "请明确填写核算金额，释放预占请填 0")
		return
	}
	if err := a.store.ManualSettle(r.Context(), id.user.ID, r.PathValue("id"), *in.Amount, in.Reason); err != nil {
		writeError(w, 400, "核算失败，请检查请求状态、金额和依据")
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}
func (a *API) settings(w http.ResponseWriter, r *http.Request, id identity) {
	days, err := a.store.LogRetention(r.Context())
	if err != nil {
		writeError(w, 500, "读取设置失败")
		return
	}
	writeJSON(w, 200, map[string]int{"log_retention_days": days})
}
func (a *API) setSettings(w http.ResponseWriter, r *http.Request, id identity) {
	var in struct {
		Days int `json:"log_retention_days"`
	}
	if !decode(w, r, &in) {
		return
	}
	if err := a.store.SetRetention(r.Context(), id.user.ID, in.Days); err != nil {
		writeError(w, 400, err.Error())
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func (a *API) revealKey(w http.ResponseWriter, r *http.Request, id identity) {
	w.Header().Set("Cache-Control", "no-store")
	target, err := targetID(r)
	if err != nil {
		writeError(w, 400, err.Error())
		return
	}
	token, err := a.store.RevealAPIKey(r.Context(), id.user.ID, target, a.cfg.SettingsKey)
	if err != nil {
		writeError(w, 404, "密钥不可复制，请创建新密钥")
		return
	}
	writeJSON(w, 200, map[string]string{"token": token})
}
