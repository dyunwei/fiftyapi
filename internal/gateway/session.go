package gateway

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"net/http"
	"strconv"
)

func sessionHash(r *http.Request, user int64, provider string, request map[string]json.RawMessage) []byte {
	value := ""
	for _, header := range []string{"session_id", "x-session-id", "conversation_id"} {
		if value = r.Header.Get(header); value != "" {
			break
		}
	}
	if value == "" {
		var metadata struct {
			UserID    string `json:"user_id"`
			SessionID string `json:"session_id"`
		}
		_ = json.Unmarshal(request["metadata"], &metadata)
		value = metadata.SessionID
		if value == "" {
			var nested struct {
				SessionID string `json:"session_id"`
			}
			if json.Unmarshal([]byte(metadata.UserID), &nested) == nil {
				value = nested.SessionID
			}
		}
	}
	if value == "" {
		_ = json.Unmarshal(request["prompt_cache_key"], &value)
	}
	if value == "" {
		for _, key := range []string{"messages", "input"} {
			var entries []json.RawMessage
			if json.Unmarshal(request[key], &entries) == nil {
				for _, entry := range entries {
					var item struct {
						Role string `json:"role"`
					}
					if json.Unmarshal(entry, &item) == nil && item.Role == "user" {
						var normalized any
						_ = json.Unmarshal(entry, &normalized)
						raw, _ := json.Marshal(normalized)
						value = string(raw)
						break
					}
				}
			}
			if value != "" {
				break
			}
		}
	}
	if value == "" {
		var input string
		if json.Unmarshal(request["input"], &input) == nil {
			value = input
		}
	}
	if value == "" {
		return nil
	}
	digest := sha256.Sum256([]byte(strconv.FormatInt(user, 10) + "\x00" + provider + "\x00" + value))
	return digest[:]
}
func legacySessionHash(r *http.Request, user int64, provider string, request map[string]json.RawMessage) []byte {
	value := r.Header.Get("session_id")
	if value == "" {
		value = r.Header.Get("x-session-id")
	}
	if value == "" {
		var metadata struct {
			UserID string `json:"user_id"`
		}
		json.Unmarshal(request["metadata"], &metadata)
		value = metadata.UserID
	}
	if value == "" {
		json.Unmarshal(request["prompt_cache_key"], &value)
	}
	if value == "" {
		for _, key := range []string{"messages", "input"} {
			var items []json.RawMessage
			if json.Unmarshal(request[key], &items) == nil && len(items) > 0 {
				var normalized any
				json.Unmarshal(items[0], &normalized)
				b, _ := json.Marshal(normalized)
				value = string(b)
				break
			}
		}
	}
	if value == "" {
		value = string(request["input"])
	}
	if value == "" {
		return nil
	}
	digest := sha256.Sum256([]byte(strconv.FormatInt(user, 10) + "\x00" + provider + "\x00" + value))
	return digest[:]
}
func sessionOffset(hash []byte, n int) int {
	if len(hash) < 8 {
		return 0
	}
	return int(binary.BigEndian.Uint64(hash[:8]) % uint64(n))
}
