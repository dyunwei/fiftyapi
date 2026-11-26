package server

import (
	"net/http"
	"time"
)

func (a *API) modelRoutes(w http.ResponseWriter, r *http.Request, _ identity) {
	q := r.URL.Query()
	model, protocol := q.Get("model"), q.Get("protocol")
	switch protocol {
	case "responses", "messages", "chat", "antigravity":
	default:
		writeError(w, 400, "协议筛选无效")
		return
	}
	if model == "" || len(model) > 255 {
		writeError(w, 400, "模型 ID 无效")
		return
	}
	ctx := r.Context()
	routes, err := a.store.ModelRouteStatus(ctx, model, protocol)
	if err != nil {
		writeError(w, 500, "读取路由状态失败")
		return
	}
	_, priceErr := a.store.Price(ctx, model)
	type item struct {
		ID       int64  `json:"id"`
		Name     string `json:"name"`
		Reason   string `json:"reason"`
		Upstream string `json:"upstream"`
		Priority int    `json:"priority"`
		Active   int    `json:"active"`
		Limit    int    `json:"limit"`
		Until    int64  `json:"until,omitempty"`
	}
	items := make([]item, 0, len(routes))
	now := time.Now().Unix()
	for _, route := range routes {
		row := item{ID: route.ID, Name: route.Name, Reason: route.Reason, Upstream: route.UpstreamID, Priority: route.Policy.Priority, Limit: route.Policy.Concurrency}
		if a.gateway != nil {
			row.Active, _ = a.gateway.AccountLoad(route.ID)
		}
		if row.Reason == "" {
			switch {
			case priceErr != nil:
				row.Reason = "unpriced"
			case route.CooldownUntil > now:
				row.Reason = "cooldown"
				row.Until = route.CooldownUntil
			case row.Active >= row.Limit:
				row.Reason = "busy"
			default:
				row.Reason = "ready"
			}
		}
		items = append(items, row)
	}
	writeJSON(w, 200, items)
}
