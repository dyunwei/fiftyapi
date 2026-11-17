package gateway

import (
	"encoding/json"
	"errors"
	"fiftyapi/internal/store"
	"net/http"
)

// No permission list for speed tiers. A request is admitted if its exact tier
// can be priced; rates come from the local catalog or an explicit manual price.
func requestedTier(request map[string]json.RawMessage, r *http.Request) (string, error) {
	tier := ""
	for _, key := range []string{"service_tier", "speed"} {
		raw := request[key]
		if len(raw) == 0 || string(raw) == "null" {
			continue
		}
		var v string
		if json.Unmarshal(raw, &v) != nil {
			return "", errors.New("服务档位必须是字符串")
		}
		if key == "speed" && v != "" {
			v = store.NormalizeTier(v)
			if v != "default" {
				tier = v
			}
		} else {
			tier = store.NormalizeTier(v)
		}
	}
	if header := r.Header.Get("OpenAI-Service-Tier"); header != "" {
		h := store.NormalizeTier(header)
		if tier != "" && tier != "default" && h != tier {
			return "", errors.New("请求头与正文的服务档位不一致")
		}
		tier = h
	}
	if len(tier) > 64 {
		return "", errors.New("服务档位无效")
	}
	return store.NormalizeTier(tier), nil
}

func observeBilling(root map[string]json.RawMessage, out *result) {
	var tier, speed string
	_ = json.Unmarshal(root["service_tier"], &tier)
	_ = json.Unmarshal(root["speed"], &speed)
	if speed != "" {
		tier = speed
	}
	if tier != "" && tier != "auto" {
		out.usage.Tier = store.NormalizeTier(tier)
		out.usage.Confirmed = true
	}
}
