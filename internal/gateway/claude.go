package gateway

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fiftyapi/internal/accounts"
	"github.com/google/uuid"
	"net/http"
	"regexp"
	"strconv"
	"strings"
)

// The default behavior follows sub2api's OAuth identity normalization and
// metadata privacy defaults. Runtime overrides are exposed in account config.
func normalizeClaude(request map[string]json.RawMessage, r *http.Request, c accounts.Credential) error {
	cfg := c.Config
	if cfg.MetadataPassthrough == nil || !*cfg.MetadataPassthrough {
		var metadata map[string]json.RawMessage
		json.Unmarshal(request["metadata"], &metadata)
		var userID string
		json.Unmarshal(metadata["user_id"], &userID)
		if cfg.FingerprintUnification == nil || *cfg.FingerprintUnification {
			var current map[string]string
			json.Unmarshal([]byte(userID), &current)
			session := current["session_id"]
			if session == "" {
				pattern := regexp.MustCompile(`(?:^|_)session_([A-Za-z0-9-]+)$`)
				if match := pattern.FindStringSubmatch(userID); len(match) > 1 {
					session = match[1]
				}
			}
			if session == "" {
				session = r.Header.Get("session_id")
			}
			if session == "" {
				var messages []json.RawMessage
				json.Unmarshal(request["messages"], &messages)
				if len(messages) > 0 {
					hash := sha256.Sum256(messages[0])
					session = uuid.UUID(hash[:16]).String()
				} else {
					session = uuid.NewString()
				}
			}
			device := sha256.Sum256([]byte("ztokenmgr:claude-account:" + strconv.FormatInt(c.AccountID, 10)))
			account := ""
			if c.Token.Account != nil {
				account = c.Token.Account.UUID
			}
			normalized, _ := json.Marshal(map[string]string{"device_id": hex.EncodeToString(device[:]), "account_uuid": account, "session_id": session})
			userID = string(normalized)
		}
		if userID != "" {
			request["metadata"], _ = json.Marshal(map[string]string{"user_id": userID})
		} else {
			delete(request, "metadata")
		}
	}
	if cfg.ClaudeSystemPromptInjection == nil || *cfg.ClaudeSystemPromptInjection {
		var blocks []map[string]json.RawMessage
		raw := request["system"]
		if len(raw) > 0 && string(raw) != "null" {
			var text string
			if json.Unmarshal(raw, &text) == nil {
				b, _ := json.Marshal(text)
				blocks = []map[string]json.RawMessage{{"type": json.RawMessage(`"text"`), "text": b}}
			} else if json.Unmarshal(raw, &blocks) != nil {
				return errors.New("Claude system 字段必须为字符串或内容块数组")
			}
		}
		identity := "You are Claude Code, Anthropic's official CLI for Claude."
		if !strings.Contains(string(raw), "You are Claude Code") {
			b, _ := json.Marshal(identity)
			blocks = append([]map[string]json.RawMessage{{"type": json.RawMessage(`"text"`), "text": b}}, blocks...)
		}
		if cfg.ClaudeSystemPrompt != "" && !strings.Contains(string(raw), cfg.ClaudeSystemPrompt) {
			b, _ := json.Marshal(cfg.ClaudeSystemPrompt)
			blocks = append(blocks, map[string]json.RawMessage{"type": json.RawMessage(`"text"`), "text": b})
		}
		request["system"], _ = json.Marshal(blocks)
	}
	return nil
}
