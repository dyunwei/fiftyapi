package antigravity

import "testing"

func TestFinalThinkingBudgetCap(t *testing.T) {
	for _, tc := range []struct {
		name, model string
		thinking    *ThinkingConfig
		want        int
	}{
		{"adaptive opus", "claude-opus-4-6-thinking", &ThinkingConfig{Type: "adaptive"}, 4096},
		{"dynamic gemini", "gemini-2.5-flash", &ThinkingConfig{Type: "enabled", BudgetTokens: -1}, 4096},
		{"large budget", "gemini-2.5-flash", &ThinkingConfig{Type: "enabled", BudgetTokens: 20000}, 4096},
		{"preserve lower", "gemini-2.5-flash", &ThinkingConfig{Type: "enabled", BudgetTokens: 2048}, 2048},
		{"disabled", "gemini-2.5-flash", &ThinkingConfig{Type: "disabled"}, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			config := buildGenerationConfigWithBudget(&ClaudeRequest{Model: tc.model, MaxTokens: 6000, Thinking: tc.thinking}, 4096)
			if tc.want == 0 {
				if config.ThinkingConfig != nil {
					t.Fatal("enabled disabled thinking")
				}
				return
			}
			if config.ThinkingConfig == nil || config.ThinkingConfig.ThinkingBudget != tc.want {
				t.Fatalf("wrong thinking config: %+v", config.ThinkingConfig)
			}
			if config.MaxOutputTokens != 6000 {
				t.Fatalf("converter inflated output budget: %d", config.MaxOutputTokens)
			}
		})
	}
}
