package accounts

import (
	"context"
	"encoding/json"
	"errors"
	"fiftyapi/internal/providers"
)

type ClientIdentity struct {
	PreviousCodexVersion string `json:"previous_codex_version,omitempty"`
	CodexVersion         string `json:"codex_version"`
	ClaudeVersion        string `json:"claude_version"`
	AntigravityVersion   string `json:"antigravity_version"`
}

func (s *Service) ClientIdentity(ctx context.Context) (ClientIdentity, error) {
	out := ClientIdentity{CodexVersion: providers.DefaultOpenAIClientVersion, ClaudeVersion: "2.1.258", AntigravityVersion: "2.23.0"}
	raw, ok, err := s.store.Setting(ctx, "client_identity")
	if err != nil {
		return out, err
	}
	if ok {
		err = json.Unmarshal([]byte(raw), &out)
	}
	return out, err
}
func (s *Service) SaveClientIdentity(ctx context.Context, actor int64, in ClientIdentity) error {
	for _, v := range []string{in.CodexVersion, in.ClaudeVersion, in.AntigravityVersion} {
		if !providers.ValidClientVersion(v) {
			return errors.New("客户端版本需为 x.y.z 或 x.y.z-alpha.N")
		}
	}
	// Serialize changes so the previous version and the new default stay paired.
	s.identityMu.Lock()
	defer s.identityMu.Unlock()
	current, err := s.ClientIdentity(ctx)
	if err != nil {
		return err
	}
	in.PreviousCodexVersion = current.PreviousCodexVersion
	if in.CodexVersion != current.CodexVersion {
		in.PreviousCodexVersion = current.CodexVersion
	}
	raw, err := json.Marshal(in)
	if err != nil {
		return err
	}
	str := string(raw)
	return s.store.SaveSetting(ctx, actor, "client_identity", &str)
}
func (s *Service) resolveIdentity(ctx context.Context, c *Credential) error {
	defaults, err := s.ClientIdentity(ctx)
	if err != nil {
		return err
	}
	if c.Config.ClientVersion == "" {
		c.inheritedVersion = true
		switch c.Config.Provider {
		case "openai":
			c.Config.ClientVersion = defaults.CodexVersion
		case "claude":
			c.Config.ClientVersion = defaults.ClaudeVersion
		}
	}
	if c.Config.Provider == "antigravity" && c.Config.AntigravityVersion == "" {
		c.inheritedAntigravity = true
		c.Config.AntigravityVersion = defaults.AntigravityVersion
	}
	c.Config = providers.NormalizeOpenAIConfig(c.Config)
	return nil
}
