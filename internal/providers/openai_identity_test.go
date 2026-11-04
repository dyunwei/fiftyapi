package providers

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

func TestOpenAICatalogVersionAndIdentityStayAligned(t *testing.T) {
	legacy := Config{Provider: "openai", ClientVersion: "0.146.0", ModelsURL: "https://chatgpt.com/backend-api/codex/models?client_version=0.146.0"}
	current := NormalizeOpenAIConfig(legacy)
	if current.ClientVersion != DefaultOpenAIClientVersion {
		t.Fatal("previous shipped defaults were not upgraded")
	}
	parsed, _ := url.Parse(current.ModelsURL)
	if parsed.Query().Get("client_version") != DefaultOpenAIClientVersion {
		t.Fatal("catalog query drifted")
	}
	headers := make(http.Header)
	IdentityHeaders(headers, current)
	if headers.Get("version") != DefaultOpenAIClientVersion || headers.Get("originator") != "codex_cli_rs" || headers.Get("User-Agent") == "" {
		t.Fatal("identity drifted")
	}
	pin := legacy
	pin.ClientVersion = "0.160.0"
	if NormalizeOpenAIConfig(pin).ClientVersion != pin.ClientVersion {
		t.Fatal("explicit version overwritten")
	}
	custom := legacy
	custom.ModelsURL = "https://example.test/models?client_version=0.146.0"
	if NormalizeOpenAIConfig(custom).ClientVersion != custom.ClientVersion {
		t.Fatal("custom endpoint pin overwritten")
	}
	custom = legacy
	custom.UserAgent = "custom-client/0.146.0"
	if NormalizeOpenAIConfig(custom).ClientVersion != custom.ClientVersion {
		t.Fatal("custom identity overwritten")
	}
	defaults, e := DefaultConfig("openai")
	if e != nil || defaults.ClientVersion != DefaultOpenAIClientVersion {
		t.Fatal("embedded defaults drifted from shared constant")
	}
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("client_version") != DefaultOpenAIClientVersion || r.Header.Get("version") != DefaultOpenAIClientVersion {
			t.Error("discovery query and identity version differ")
		}
		io.WriteString(w, `{"models":[{"slug":"gpt-6.1-sol"},{"slug":"gpt-6.1-terra"}]}`)
	}))
	defer upstream.Close()
	config := Config{Provider: "openai", ModelsURL: upstream.URL + "/models"}
	models, e := Models(context.Background(), upstream.Client(), config, Token{AccessToken: "fixture-token"})
	if e != nil || len(models) != 2 || models[0] != "gpt-6.1-sol" {
		t.Fatal("new model discovery failed")
	}
}
