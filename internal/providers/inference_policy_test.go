package providers

import "testing"

func TestInferencePolicyValidation(t *testing.T) {
	for _, p := range []InferencePolicy{{}, {MaxEffort: "medium", MaxThinkingTokens: 1024, ForceStandard: true}, {MaxEffort: "high", MaxThinkingTokens: 1048576}} {
		if err := p.Validate(); err != nil {
			t.Fatal(err)
		}
	}
	for _, p := range []InferencePolicy{{MaxEffort: "auto"}, {MaxThinkingTokens: -1}, {MaxThinkingTokens: 1023}, {MaxThinkingTokens: 1048577}} {
		if err := (Config{InferencePolicy: p}).Validate(); err == nil {
			t.Fatalf("invalid policy accepted: %+v", p)
		}
		if err := p.Validate(); err == nil {
			t.Fatalf("invalid policy accepted: %+v", p)
		}
	}
}
