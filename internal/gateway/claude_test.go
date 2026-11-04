package gateway

import (
	"encoding/json"
	"fiftyapi/internal/accounts"
	"fiftyapi/internal/providers"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestClaudeNormalizationPrivacyAndSessionContinuity(t *testing.T) {
	build := func() map[string]json.RawMessage {
		var request map[string]json.RawMessage
		json.Unmarshal([]byte(`{"messages":[{"role":"user","content":"hello"}],"metadata":{"private_note":"remove-me","user_id":"user_device_account_old_session_abc-123"},"system":"Keep the user instructions."}`), &request)
		return request
	}
	c := accounts.Credential{AccountID: 42}
	r := httptest.NewRequest("POST", "/v1/messages", nil)
	a, b := build(), build()
	if err := normalizeClaude(a, r, c); err != nil {
		t.Fatal(err)
	}
	if err := normalizeClaude(b, r, c); err != nil {
		t.Fatal(err)
	}
	if string(a["metadata"]) != string(b["metadata"]) {
		t.Fatal("identity changed for same account/session")
	}
	if strings.Contains(string(a["metadata"]), "remove-me") {
		t.Fatal("private metadata leaked")
	}
	var metadata map[string]string
	json.Unmarshal(a["metadata"], &metadata)
	var identity map[string]string
	json.Unmarshal([]byte(metadata["user_id"]), &identity)
	if identity["session_id"] != "abc-123" || len(identity["device_id"]) != 64 {
		t.Fatal("session or account identity lost")
	}
	if !strings.Contains(string(a["system"]), "Claude Code") || !strings.Contains(string(a["system"]), "Keep the user instructions.") {
		t.Fatal("system blocks lost")
	}
	c.AccountID = 43
	if err := normalizeClaude(b, r, c); err != nil {
		t.Fatal(err)
	}
	if string(a["metadata"]) == string(b["metadata"]) {
		t.Fatal("different upstream accounts share identity")
	}
	passthrough, disabled := true, false
	c.Config = providers.Config{MetadataPassthrough: &passthrough, ClaudeSystemPromptInjection: &disabled}
	original := build()
	before, _ := json.Marshal(original)
	if err := normalizeClaude(original, r, c); err != nil {
		t.Fatal(err)
	}
	after, _ := json.Marshal(original)
	if string(before) != string(after) {
		t.Fatal("explicit passthrough settings ignored")
	}
}
