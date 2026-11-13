package providers

import (
	"encoding/json"
	"net/http"
)

// NormalizeOpenAIDevice changes device identity only. Session/thread/turn IDs
// and application metadata stay independent for every real conversation.
func NormalizeOpenAIDevice(h http.Header, request map[string]json.RawMessage, device string) {
	if device == "" {
		return
	}
	h.Set("x-codex-installation-id", device)
	if raw := h.Get("x-codex-turn-metadata"); raw != "" {
		h.Set("x-codex-turn-metadata", normalizeDeviceMetadata(raw, device))
	}
	var metadata map[string]json.RawMessage
	if json.Unmarshal(request["client_metadata"], &metadata) != nil || metadata == nil {
		metadata = map[string]json.RawMessage{}
	}
	metadata["x-codex-installation-id"], _ = json.Marshal(device)
	// Some clients also send the unprefixed spelling.
	if _, ok := metadata["installation_id"]; ok {
		metadata["installation_id"], _ = json.Marshal(device)
	}
	if raw, ok := metadata["x-codex-turn-metadata"]; ok {
		var text string
		if json.Unmarshal(raw, &text) == nil {
			metadata["x-codex-turn-metadata"], _ = json.Marshal(normalizeDeviceMetadata(text, device))
		} else {
			metadata["x-codex-turn-metadata"] = json.RawMessage(normalizeDeviceMetadata(string(raw), device))
		}
	}
	request["client_metadata"], _ = json.Marshal(metadata)
}

func normalizeDeviceMetadata(raw, device string) string {
	var fields map[string]json.RawMessage
	if json.Unmarshal([]byte(raw), &fields) != nil || fields == nil {
		fields = map[string]json.RawMessage{}
	}
	fields["installation_id"], _ = json.Marshal(device)
	if _, ok := fields["x-codex-installation-id"]; ok {
		fields["x-codex-installation-id"], _ = json.Marshal(device)
	}
	encoded, _ := json.Marshal(fields)
	return string(encoded)
}
