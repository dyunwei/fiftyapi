package providers

import (
	"errors"
	"net/url"
)

// AntigravityInferenceEndpoint keeps account discovery and native inference
// routing separate. Custom inference URLs are explicit and are never replaced
// or retried against an official origin.
func AntigravityInferenceEndpoint(c Config, stream bool) (string, error) {
	raw := c.GatewayURL
	if raw != "" {
		u, err := url.Parse(raw)
		if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.Fragment != "" || u.Opaque != "" {
			return "", errors.New("推理端点无效")
		}
		return u.String(), nil
	}
	u, err := url.Parse(c.ModelsURL)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.Fragment != "" || u.Opaque != "" {
		return "", errors.New("推理端点无效")
	}
	// Preserve the inference origin used before the native-forwarding refactor.
	// Only this exact official discovery host receives the official mapping.
	if u.Host == "cloudcode-pa.googleapis.com" {
		u.Host = "daily-cloudcode-pa.googleapis.com"
	}
	u.Path = "/v1internal:generateContent"
	u.RawPath = ""
	u.RawQuery = ""
	if stream {
		u.Path = "/v1internal:streamGenerateContent"
		u.RawQuery = "alt=sse"
	}
	return u.String(), nil
}
