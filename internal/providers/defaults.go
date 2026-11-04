package providers

import (
	"embed"
	"encoding/json"
	"errors"
	compat "fiftyapi/internal/compat/antigravity"
	"os"
	"strings"
)

//go:embed defaults/*.json
var defaultFiles embed.FS

func DefaultConfig(provider string) (Config, error) {
	if provider != "claude" && provider != "openai" && provider != "antigravity" {
		return Config{}, errors.New("不支持的供应商")
	}
	b, err := defaultFiles.ReadFile("defaults/" + provider + ".json")
	if err != nil {
		return Config{}, err
	}
	var c Config
	err = json.Unmarshal(b, &c)
	if provider == "antigravity" {
		c.AntigravityVersion = os.Getenv("ANTIGRAVITY_USER_AGENT_VERSION")
		c.AntigravityIdentity = compat.GetDefaultIdentityPatch()
	}
	return c, err
}
func ClientSecret(c Config) string {
	if c.ClientSecretEnv != "" {
		if value := strings.TrimSpace(os.Getenv(c.ClientSecretEnv)); value != "" {
			return value
		}
	}
	if c.Provider == "antigravity" {
		if value := strings.TrimSpace(os.Getenv("ANTIGRAVITY_OAUTH_CLIENT_SECRET")); value != "" {
			return value
		}
	}
	if c.Provider == "antigravity" && c.ClientID == "1071006060591-tmhssin2h21lcre235vtolojh4g403ep.apps.googleusercontent.com" {
		return antigravityDesktopClientSecret
	}
	return ""
}
