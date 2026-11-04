package providers

import "errors"

// InferencePolicy is opt-in and belongs to the selected upstream account.
type InferencePolicy struct {
	MaxEffort         string `json:"max_effort,omitempty"`
	MaxThinkingTokens int    `json:"max_thinking_tokens,omitempty"`
	ForceStandard     bool   `json:"force_standard,omitempty"`
}

func (p InferencePolicy) Validate() error {
	if p.MaxEffort != "" && p.MaxEffort != "low" && p.MaxEffort != "medium" && p.MaxEffort != "high" {
		return errors.New("推理强度上限应为 low、medium 或 high")
	}
	if p.MaxThinkingTokens != 0 && (p.MaxThinkingTokens < 1024 || p.MaxThinkingTokens > 1048576) {
		return errors.New("思考预算上限应为 1024–1048576，0 表示不限制")
	}
	return nil
}
