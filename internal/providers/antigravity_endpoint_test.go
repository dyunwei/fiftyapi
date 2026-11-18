package providers

import "testing"

func TestAntigravityNativeInferenceEndpoint(t *testing.T) {
	for _, tc := range []struct {
		name    string
		c       Config
		stream  bool
		want    string
		invalid bool
	}{
		{"official discovery is separate", Config{ModelsURL: "https://cloudcode-pa.googleapis.com/v1internal:fetchAvailableModels"}, true, "https://daily-cloudcode-pa.googleapis.com/v1internal:streamGenerateContent?alt=sse", false},
		{"native JSON", Config{ModelsURL: "https://cloudcode-pa.googleapis.com/v1internal:fetchAvailableModels"}, false, "https://daily-cloudcode-pa.googleapis.com/v1internal:generateContent", false},
		{"daily remains daily", Config{ModelsURL: "https://daily-cloudcode-pa.googleapis.com/models"}, true, "https://daily-cloudcode-pa.googleapis.com/v1internal:streamGenerateContent?alt=sse", false},
		{"custom discovery never escapes", Config{ModelsURL: "https://private.example/models?catalog=1"}, true, "https://private.example/v1internal:streamGenerateContent?alt=sse", false},
		{"custom inference remains exact", Config{ModelsURL: "https://cloudcode-pa.googleapis.com/models", GatewayURL: "https://private.example/native?action=stream"}, true, "https://private.example/native?action=stream", false},
		{"insecure", Config{ModelsURL: "http://private.example/models"}, true, "", true},
		{"embedded credentials", Config{GatewayURL: "https://operator:secret@private.example/infer"}, true, "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := AntigravityInferenceEndpoint(tc.c, tc.stream)
			if (err != nil) != tc.invalid || got != tc.want {
				t.Fatalf("got %q err %v; want %q", got, err, tc.want)
			}
		})
	}
}
