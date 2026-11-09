package modelref

import (
	"fmt"
	"strings"
)

type Mapping struct {
	ID         string   `json:"id"`
	UpstreamID string   `json:"upstream_id"`
	Protocol   string   `json:"protocol,omitempty"`
	Protocols  []string `json:"protocols,omitempty"`
}

func GatewayProvider(account int64, protocol string) string {
	return fmt.Sprintf("gateway/%d/%s", account, protocol)
}
func Protocol(provider string) string {
	if strings.HasPrefix(provider, "gateway/") {
		parts := strings.Split(provider, "/")
		if len(parts) >= 3 && (parts[2] == "messages" || parts[2] == "responses" || parts[2] == "chat") {
			return parts[2]
		}
		return ""
	}
	if provider == "openai" {
		return "responses"
	}
	if provider == "claude" || provider == "antigravity" {
		return "messages"
	}
	return ""
}

func ValidProtocol(p string) bool { return p == "chat" || p == "messages" || p == "responses" }
func (m Mapping) SupportedProtocols() []string {
	if len(m.Protocols) > 0 {
		return m.Protocols
	}
	if m.Protocol != "" {
		return []string{m.Protocol}
	}
	return nil
}
