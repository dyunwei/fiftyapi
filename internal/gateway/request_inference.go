package gateway

import (
	"encoding/json"
	"fiftyapi/internal/store"
)

// Observe only explicit, protocol-specific client settings. Logging must never
// validate or mutate the request, infer model defaults, or retain arbitrary JSON.
func requestInference(request map[string]json.RawMessage, protocol string) store.RequestInference {
	readEnum := func(raw json.RawMessage) string {
		var value string
		if json.Unmarshal(raw, &value) != nil || len(value) > 32 {
			return ""
		}
		for _, c := range value {
			if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '_' || c == '-') {
				return ""
			}
		}
		return value
	}
	object := func(raw json.RawMessage) map[string]json.RawMessage {
		var value map[string]json.RawMessage
		_ = json.Unmarshal(raw, &value)
		return value
	}
	out := store.RequestInference{ServiceTier: readEnum(request["service_tier"])}
	switch protocol {
	case "responses":
		out.ReasoningEffort = readEnum(object(request["reasoning"])["effort"])
	case "chat":
		out.ReasoningEffort = readEnum(request["reasoning_effort"])
	case "messages":
		out.ReasoningEffort = readEnum(object(request["output_config"])["effort"])
		thinking := object(request["thinking"])
		out.ThinkingType = readEnum(thinking["type"])
		var budget int64
		if json.Unmarshal(thinking["budget_tokens"], &budget) == nil && budget > 0 {
			out.ThinkingBudgetTokens = &budget
		}
		out.Speed = readEnum(request["speed"])
	}
	return out
}
