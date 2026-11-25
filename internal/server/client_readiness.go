package server

import (
	"fiftyapi/internal/store"
	"net/http"
)

// clientReadiness reports the first catalog or budget blocker for the signed-in
// user. It makes no upstream requests and returns no account or model details.
func (a *API) clientReadiness(w http.ResponseWriter, r *http.Request, id identity) {
	protocol := r.URL.Query().Get("protocol")
	switch protocol {
	case "responses", "messages", "chat":
	default:
		writeError(w, 400, "协议筛选无效")
		return
	}
	selected := r.URL.Query().Get("model")
	if len(selected) > 255 {
		writeError(w, 400, "模型 ID 无效")
		return
	}
	ctx := r.Context()
	models, err := a.store.Models(ctx)
	if err != nil {
		writeError(w, 500, "读取模型失败")
		return
	}
	access, err := a.store.UserAccess(ctx, id.user.ID)
	if err != nil {
		writeError(w, 500, "读取用户访问限制失败")
		return
	}
	var routed, enabled, priced, allowed, matching, found, compatible bool
	for _, model := range models {
		if selected != "" && model.ID != selected {
			continue
		}
		if selected != "" && !access.Allows(model.ID) {
			continue
		}
		found = true
		if model.SupportsProtocol(protocol) {
			compatible = true
		}
	}
	for _, model := range models {
		if selected != "" && (model.ID != selected || !access.Allows(model.ID)) {
			continue
		}
		if compatible && !model.SupportsProtocol(protocol) {
			continue
		}
		if !model.Available {
			continue
		}
		routed = true
		if !model.Enabled {
			continue
		}
		enabled = true
		if model.Price == nil {
			continue
		}
		priced = true
		if !access.Allows(model.ID) {
			continue
		}
		allowed = true
		matching = matching || model.SupportsProtocol(protocol)
	}
	code := "ready"
	blocks := []store.QuotaBlock{}
	switch {
	case selected != "" && !found:
		code = "model_unknown"
	case selected != "" && !compatible:
		code = "protocol"
	case !routed:
		code = "upstream"
	case !enabled:
		code = "model_disabled"
	case !priced:
		code = "price"
	case !allowed:
		code = "access"
	default:
		windows, err := a.store.Quota(ctx, id.user.ID)
		if err != nil {
			writeError(w, 500, "读取额度失败")
			return
		}
		for _, window := range windows {
			if window.Limit <= 0 {
				code = "budget_unassigned"
				blocks = append(blocks, store.QuotaBlock{Window: window.Kind})
			} else if window.Limit-window.Used-window.Reserved <= 0 {
				if code != "budget_unassigned" {
					code = "budget_exhausted"
				}
				blocks = append(blocks, store.QuotaBlock{Window: window.Kind, ResetAt: window.ExpiresAt})
			}
		}
		if code == "ready" && !matching {
			code = "protocol"
		}
	}
	writeJSON(w, 200, map[string]any{"code": code, "quota": blocks})
}
