package providers

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/url"
	"regexp"
	"strings"
)

// DefaultOpenAIClientVersion is the locally verified Codex identity used for
// discovery and inference together. Keep defaults/openai.json in sync.
const DefaultOpenAIClientVersion = "0.164.0"

// NormalizeOpenAIConfig upgrades the previous shipped official defaults only.
// Custom endpoints, custom user agents and other explicit versions remain pins.
func NormalizeOpenAIConfig(c Config) Config {
	if c.Provider != "openai" {
		return c
	}
	u, err := url.Parse(c.ModelsURL)
	legacy := err == nil && u.Scheme == "https" && u.Host == "chatgpt.com" && u.Path == "/backend-api/codex/models" && u.Query().Get("client_version") == "0.146.0" && c.ClientVersion == "0.146.0" && c.UserAgent == ""
	if c.ClientVersion == "" || legacy {
		c.ClientVersion = DefaultOpenAIClientVersion
	}
	if err == nil {
		q := u.Query()
		q.Set("client_version", c.ClientVersion)
		u.RawQuery = q.Encode()
		c.ModelsURL = u.String()
	}
	return c
}

func IdentityHeaders(h http.Header, c Config) {
	c = NormalizeOpenAIConfig(c)
	version := c.ClientVersion
	switch c.Provider {
	case "openai":
		if version == "" {
			version = DefaultOpenAIClientVersion
		}
		ua := c.UserAgent
		if ua == "" {
			ua = "codex_cli_rs/" + version + " (Ubuntu 22.4.0; x86_64) xterm-256color"
		}
		h.Set("User-Agent", ua)
		h.Set("originator", "codex_cli_rs")
		h.Set("version", version)
	case "claude":
		if version == "" {
			version = "2.1.258"
		}
		ua := c.UserAgent
		if ua == "" {
			ua = "claude-cli/" + version + " (external, cli)"
		}
		h.Set("User-Agent", ua)
		h.Set("anthropic-version", "2023-06-01")
		h.Set("anthropic-beta", "claude-code-20250219,oauth-2025-04-20,interleaved-thinking-2025-05-14,fine-grained-tool-streaming-2025-05-14")
		h.Set("x-app", "cli")
		h.Set("x-stainless-lang", "js")
		h.Set("x-stainless-package-version", "0.94.0")
		h.Set("x-stainless-runtime", "node")
		h.Set("x-stainless-runtime-version", "v24.3.0")
		h.Set("x-stainless-os", "Linux")
		h.Set("x-stainless-arch", "arm64")
		h.Set("x-stainless-retry-count", "0")
		h.Set("anthropic-dangerous-direct-browser-access", "true")
	}
}

// JWT claims are only supplemental account metadata from already exchanged
// credentials. They are never used as proof of authentication or role.
func OpenAIAccountID(t Token) string {
	for _, token := range []string{t.AccessToken, t.IDToken} {
		parts := strings.Split(token, ".")
		if len(parts) < 2 {
			continue
		}
		payload, e := base64.RawURLEncoding.DecodeString(parts[1])
		if e != nil {
			continue
		}
		var claims map[string]json.RawMessage
		if json.Unmarshal(payload, &claims) != nil {
			continue
		}
		var auth struct {
			ID string `json:"chatgpt_account_id"`
		}
		if json.Unmarshal(claims["https://api.openai.com/auth"], &auth) == nil && auth.ID != "" {
			return auth.ID
		}
	}
	return ""
}

func SetOpenAIAccountHeader(h http.Header, c Config, t Token) {
	id := c.OpenAIAccountID
	if id == "" {
		id = OpenAIAccountID(t)
	}
	if id != "" {
		h.Set("chatgpt-account-id", id)
	}
}
func OpenAIIdentity(t Token) (email, plan string) {
	for _, raw := range []string{t.IDToken, t.AccessToken} {
		parts := strings.Split(raw, ".")
		if len(parts) < 2 {
			continue
		}
		b, err := base64.RawURLEncoding.DecodeString(parts[1])
		if err != nil {
			continue
		}
		var claims struct {
			Email   string `json:"email"`
			Profile struct {
				Email string `json:"email"`
			} `json:"https://api.openai.com/profile"`
			Auth struct {
				Plan string `json:"chatgpt_plan_type"`
			} `json:"https://api.openai.com/auth"`
		}
		if json.Unmarshal(b, &claims) != nil {
			continue
		}
		if email == "" {
			email = claims.Email
			if email == "" {
				email = claims.Profile.Email
			}
		}
		if plan == "" {
			plan = claims.Auth.Plan
		}
	}
	if len(email) > 254 {
		email = ""
	}
	if len(plan) > 64 {
		plan = ""
	}
	return
}

var clientVersionPattern = regexp.MustCompile(`^[0-9]+[.][0-9]+[.][0-9]+(-[0-9A-Za-z]+([.][0-9A-Za-z]+)*)?$`)

func ValidClientVersion(version string) bool {
	return len(version) <= 64 && clientVersionPattern.MatchString(version)
}
