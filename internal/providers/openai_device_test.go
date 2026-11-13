package providers

import (
	"encoding/json"
	"net/http"
	"testing"
)

func TestOpenAIDevicePreservesConversationIdentity(t *testing.T) {
	for _, embedded := range []string{`"{\"installation_id\":\"client\",\"thread_id\":\"thread\"}"`, `{"installation_id":"client","thread_id":"thread"}`, `null`} {
		h := http.Header{}
		h.Set("session-id", "session")
		h.Set("x-codex-turn-metadata", `{"installation_id":"client","session_id":"session","turn_id":"turn","sandbox":"restricted"}`)
		var body map[string]json.RawMessage
		if err := json.Unmarshal([]byte(`{"client_metadata":{"installation_id":"client","session_id":"session","thread_id":"thread","custom":"keep","x-codex-turn-metadata":`+embedded+`},"input":[{"type":"reasoning","encrypted_content":"opaque"}],"prompt_cache_key":"cache"}`), &body); err != nil {
			t.Fatal(err)
		}
		NormalizeOpenAIDevice(h, body, "account-device")
		if h.Get("x-codex-installation-id") != "account-device" || h.Get("session-id") != "session" {
			t.Fatal("device/session header identity incorrect")
		}
		var turn map[string]string
		json.Unmarshal([]byte(h.Get("x-codex-turn-metadata")), &turn)
		if turn["installation_id"] != "account-device" || turn["session_id"] != "session" || turn["turn_id"] != "turn" || turn["sandbox"] != "restricted" {
			t.Fatal("turn metadata identity or application fields changed")
		}
		var metadata map[string]json.RawMessage
		json.Unmarshal(body["client_metadata"], &metadata)
		if string(metadata["x-codex-installation-id"]) != `"account-device"` || string(metadata["installation_id"]) != `"account-device"` || string(metadata["session_id"]) != `"session"` || string(metadata["thread_id"]) != `"thread"` || string(metadata["custom"]) != `"keep"` || string(body["prompt_cache_key"]) != `"cache"` || string(body["input"]) != `[{"type":"reasoning","encrypted_content":"opaque"}]` {
			t.Fatal("device normalization changed conversation state")
		}
		var text string
		raw := metadata["x-codex-turn-metadata"]
		if json.Unmarshal(raw, &text) == nil {
			raw = []byte(text)
		}
		var nested map[string]string
		json.Unmarshal(raw, &nested)
		if nested["installation_id"] != "account-device" || (embedded != `null` && nested["thread_id"] != "thread") {
			t.Fatal("embedded device metadata incorrect")
		}
	}
}
