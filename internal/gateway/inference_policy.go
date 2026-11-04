package gateway

import (
	"encoding/json"
	"errors"
	"fiftyapi/internal/providers"
)

var effortRanks = map[string]int{"none": 0, "minimal": 1, "low": 2, "medium": 3, "high": 4, "xhigh": 5, "max": 6, "ultra": 7}

func policyObject(raw json.RawMessage) (map[string]json.RawMessage, error) {
	obj := map[string]json.RawMessage{}
	if len(raw) != 0 && string(raw) != "null" {
		if err := json.Unmarshal(raw, &obj); err != nil || obj == nil {
			return nil, errors.New("推理配置必须是 JSON 对象")
		}
	}
	return obj, nil
}

func capEffort(obj map[string]json.RawMessage, field, ceiling string) error {
	var effort string
	raw := obj[field]
	if len(raw) > 0 && string(raw) != "null" {
		if json.Unmarshal(raw, &effort) != nil {
			return errors.New("推理强度必须是字符串")
		}
	}
	rank, known := effortRanks[effort]
	if effort != "" && !known {
		return errors.New("账号已设置推理上限，无法识别请求中的推理强度")
	}
	// An omitted effort can select an expensive model default, so set the cap.
	if effort == "" || rank > effortRanks[ceiling] {
		obj[field], _ = json.Marshal(ceiling)
	}
	return nil
}

func capObjectEffort(request map[string]json.RawMessage, field, ceiling string) error {
	obj, err := policyObject(request[field])
	if err != nil {
		return err
	}
	if err = capEffort(obj, "effort", ceiling); err != nil {
		return err
	}
	request[field], _ = json.Marshal(obj)
	return nil
}

// Each forwarding attempt gets its own map. Never mutate raw JSON shared by retries.
func applyInferencePolicy(request map[string]json.RawMessage, protocol string, p providers.InferencePolicy, antigravity bool) error {
	if err := p.Validate(); err != nil {
		return err
	}
	if protocol == "responses" || protocol == "chat" {
		if p.ForceStandard {
			request["service_tier"] = json.RawMessage(`"default"`)
		}
		var tier string
		if raw := request["service_tier"]; len(raw) > 0 && string(raw) != "null" {
			if json.Unmarshal(raw, &tier) != nil {
				return errors.New("service_tier 必须是字符串")
			}
		}
		if tier != "" && tier != "auto" && tier != "default" {
			return errors.New("当前折算价格适用于标准档位，请使用 auto/default")
		}
		if p.MaxEffort != "" {
			if protocol == "chat" {
				return capEffort(request, "reasoning_effort", p.MaxEffort)
			}
			return capObjectEffort(request, "reasoning", p.MaxEffort)
		}
		return nil
	}
	if protocol != "messages" {
		return nil
	}
	if p.ForceStandard {
		// Omitting speed selects standard processing; do not send an unsupported enum.
		delete(request, "speed")
		if !antigravity {
			request["service_tier"] = json.RawMessage(`"standard_only"`)
		}
	}
	if p.MaxEffort != "" && !antigravity {
		// Legacy enabled/disabled thinking models may not support effort. Only add
		// it for adaptive/default requests, or clamp an effort the caller uses.
		thinking, err := policyObject(request["thinking"])
		if err != nil {
			return err
		}
		var kind string
		_ = json.Unmarshal(thinking["type"], &kind)
		if len(request["output_config"]) > 0 || kind == "adaptive" || kind == "" {
			if err := capObjectEffort(request, "output_config", p.MaxEffort); err != nil {
				return err
			}
		}
		// Per-message effort can override the top-level setting on newer models.
		if raw := request["messages"]; len(raw) > 0 {
			var messages []map[string]json.RawMessage
			if json.Unmarshal(raw, &messages) != nil {
				return errors.New("messages 配置无效")
			}
			for _, message := range messages {
				if len(message["output_config"]) > 0 {
					if err := capObjectEffort(message, "output_config", p.MaxEffort); err != nil {
						return err
					}
				}
			}
			request["messages"], _ = json.Marshal(messages)
		}
	}
	if p.MaxThinkingTokens > 0 && !antigravity {
		thinking, err := policyObject(request["thinking"])
		if err != nil {
			return err
		}
		var kind string
		_ = json.Unmarshal(thinking["type"], &kind)
		if kind == "enabled" {
			var budget int
			if raw := thinking["budget_tokens"]; len(raw) > 0 && json.Unmarshal(raw, &budget) != nil {
				return errors.New("思考预算必须是整数")
			}
			if budget <= 0 || budget > p.MaxThinkingTokens {
				thinking["budget_tokens"], _ = json.Marshal(p.MaxThinkingTokens)
			}
			request["thinking"], _ = json.Marshal(thinking)
		}
	}
	return nil
}

// Antigravity's current adapter uses token budgets rather than effort levels.
// These are gateway policy mappings, not vendor-defined effort equivalences.
func antigravityBudget(p providers.InferencePolicy) int {
	cap := p.MaxThinkingTokens
	if effortCap := map[string]int{"low": 2048, "medium": 8192, "high": 24576}[p.MaxEffort]; effortCap > 0 && (cap == 0 || effortCap < cap) {
		cap = effortCap
	}
	return cap
}
