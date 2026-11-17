package server

import (
	"context"
	"encoding/json"
	"fiftyapi/internal/accounts"
	"fiftyapi/internal/pricing"
	"net/http"
	"time"
)

func (a *API) maintenanceSettings(w http.ResponseWriter, r *http.Request, id identity) {
	if a.accounts == nil {
		writeError(w, 503, "账号服务不可用")
		return
	}
	identity, err := a.accounts.ClientIdentity(r.Context())
	if err != nil {
		writeError(w, 500, "读取设置失败")
		return
	}
	prices, err := a.store.MaintenanceState(r.Context(), "prices")
	if err != nil {
		writeError(w, 500, "读取设置失败")
		return
	}
	versions, err := a.store.MaintenanceState(r.Context(), "codex_version")
	if err != nil {
		writeError(w, 500, "读取设置失败")
		return
	}
	var candidate pricing.VersionCandidate
	if raw, ok, e := a.store.Setting(r.Context(), "codex_candidate"); e == nil && ok {
		_ = json.Unmarshal([]byte(raw), &candidate)
	}
	var count int
	_ = a.store.DB.QueryRowContext(r.Context(), "SELECT count(*) FROM price_catalog").Scan(&count)
	writeJSON(w, 200, map[string]any{"identity": identity, "prices": prices, "versions": versions, "candidate": candidate, "catalog_count": count})
}
func (a *API) saveMaintenanceSettings(w http.ResponseWriter, r *http.Request, id identity) {
	if a.accounts == nil {
		writeError(w, 503, "账号服务不可用")
		return
	}
	var in accounts.ClientIdentity
	if !decode(w, r, &in) {
		return
	}
	if err := a.accounts.SaveClientIdentity(r.Context(), id.user.ID, in); err != nil {
		writeError(w, 400, err.Error())
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}
func (a *API) syncPrices(w http.ResponseWriter, r *http.Request, id identity) {
	ctx, cancel := context.WithTimeout(r.Context(), 45*time.Second)
	defer cancel()
	err := pricing.New(a.store).Sync(ctx)
	note := ""
	if err != nil {
		note = err.Error()
	}
	_ = a.store.RecordMaintenance(ctx, "prices", note)
	if err != nil {
		writeError(w, 502, note)
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}
func (a *API) checkCodex(w http.ResponseWriter, r *http.Request, id identity) {
	ctx, cancel := context.WithTimeout(r.Context(), 35*time.Second)
	defer cancel()
	err := pricing.New(a.store).CheckCodex(ctx)
	note := ""
	if err != nil {
		note = err.Error()
	}
	_ = a.store.RecordMaintenance(ctx, "codex_version", note)
	if err != nil {
		writeError(w, 502, note)
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}
func (a *API) setPricingKey(w http.ResponseWriter, r *http.Request, id identity) {
	var in struct {
		Model string `json:"model"`
		Key   string `json:"key"`
	}
	if !decode(w, r, &in) {
		return
	}
	if err := a.store.SetPricingKey(r.Context(), id.user.ID, in.Model, in.Key); err != nil {
		writeError(w, 400, err.Error())
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}
func (a *API) priceCatalog(w http.ResponseWriter, r *http.Request, id identity) {
	search := r.URL.Query().Get("search")
	if len(search) > 512 {
		writeError(w, 400, "搜索内容过长")
		return
	}
	rows, err := a.store.DB.QueryContext(r.Context(), "SELECT key,snapshot,synced_at FROM price_catalog WHERE instr(lower(key),lower(?))>0 ORDER BY key LIMIT 100", search)
	if err != nil {
		writeError(w, 500, "读取价格目录失败")
		return
	}
	defer rows.Close()
	items := []any{}
	for rows.Next() {
		var key, raw string
		var synced int64
		if rows.Scan(&key, &raw, &synced) != nil {
			writeError(w, 500, "读取价格目录失败")
			return
		}
		items = append(items, map[string]any{"key": key, "price": json.RawMessage(raw), "synced_at": synced})
	}
	if rows.Err() != nil {
		writeError(w, 500, "读取价格目录失败")
		return
	}
	writeJSON(w, 200, items)
}
