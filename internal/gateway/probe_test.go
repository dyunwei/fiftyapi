package gateway

import (
	"context"
	"encoding/json"
	"fiftyapi/internal/accounts"
	"fiftyapi/internal/providers"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestProbePinsModelAndValidatesProtocol(t *testing.T) {
	calls := 0
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		if body["model"] != "upstream-model" || r.URL.Path != "/v1/chat/completions" {
			t.Error("incorrect model or protocol", body, r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer upstream-private-test-key" {
			t.Error("missing upstream auth")
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"Hello"}}],"usage":{"prompt_tokens":1,"completion_tokens":1}}`))
	}))
	defer upstream.Close()
	g := setupRelay(t, upstream, "custom", "chat")
	for _, tc := range []struct{ model, protocol string }{{"missing", "chat"}, {"model", "responses"}} {
		if _, err := g.h.Probe(context.Background(), g.account, tc.model, tc.protocol, "localhost"); err == nil {
			t.Error("invalid probe accepted")
		}
	}
	if calls != 0 {
		t.Fatal("invalid probe contacted upstream")
	}
	result, err := g.h.Probe(context.Background(), g.account, "model", "chat", "localhost")
	if err != nil || result.Error != "" || result.Status != 200 || !strings.Contains(result.Response, "Hello") {
		t.Fatalf("probe failed: %+v %v", result, err)
	}
	if calls != 1 {
		t.Fatalf("calls=%d", calls)
	}
	if strings.Contains(string(result.Request)+result.Response, "upstream-private-test-key") {
		t.Fatal("credential leaked")
	}
}

func TestProbeWriterBoundsResponse(t *testing.T) {
	w := &probeWriter{}
	body := []byte(strings.Repeat("x", 300<<10))
	n, err := w.Write(body)
	if err != nil || n != len(body) || w.body.Len() != 256<<10 || !w.truncated {
		t.Fatal("response limit failed")
	}
}

type probeFixtureTransport func(*http.Request) (*http.Response, error)

func (f probeFixtureTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestAntigravityProbeNativeEnvelopeAndSafe429Diagnostics(t *testing.T) {
	calls := 0
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		var body struct {
			Model, Project, RequestID, RequestType, UserAgent string
			Request                                           struct {
				Contents   []json.RawMessage `json:"contents"`
				Generation map[string]int    `json:"generationConfig"`
			} `json:"request"`
			Messages json.RawMessage `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		if body.Model != "native-probe-model" || body.Project != "private-project" || !strings.HasPrefix(body.RequestID, "agent-") || body.RequestType != "agent" || body.UserAgent != "antigravity" || len(body.Messages) > 0 || len(body.Request.Contents) != 1 || body.Request.Generation["maxOutputTokens"] != 64 {
			t.Error("native probe envelope missing or converted")
		}
		if r.URL.Path != "/v1internal:streamGenerateContent" || r.URL.Query().Get("alt") != "sse" {
			t.Error("incorrect native inference path")
		}
		if r.Header.Get("Authorization") != "Bearer private-probe-access" {
			t.Error("account authentication missing")
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Retry-After", "20")
		w.WriteHeader(429)
		io.WriteString(w, `{"error":{"code":429,"status":"RESOURCE_EXHAUSTED","message":"private-probe-access private-project private error message","details":[{"@type":"type.googleapis.com/google.rpc.ErrorInfo","reason":"RATE_LIMIT_EXCEEDED","metadata":{"project":"private-project"}},{"@type":"type.googleapis.com/google.rpc.RetryInfo","retryDelay":"30s"}]}}`)
	}))
	defer upstream.Close()
	g := setupGateway(t, "antigravity", upstream)
	ctx := context.Background()
	cfg, err := providers.DefaultConfig("antigravity")
	if err != nil {
		t.Fatal(err)
	}
	cfg.ModelsBody = json.RawMessage(`{"project":"private-project"}`)
	account, err := g.h.accounts.Import(ctx, g.user, "native probe", accounts.Credential{Config: cfg, Token: providers.Token{AccessToken: "private-probe-access"}, ExpiresAt: time.Now().Add(time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	if err = g.s.SyncModels(ctx, account, "antigravity", []string{"native-probe-model"}); err != nil {
		t.Fatal(err)
	}
	// Every official URL is intercepted and rewritten to this local TLS fixture.
	// No DNS lookup or real-provider request is permitted by the transport.
	g.h.client = func(providers.Config) *http.Client {
		return &http.Client{Transport: probeFixtureTransport(func(r *http.Request) (*http.Response, error) {
			if r.URL.Host != "daily-cloudcode-pa.googleapis.com" {
				t.Errorf("probe used discovery host for inference: %s", r.URL.Host)
			}
			clone := r.Clone(r.Context())
			u := *r.URL
			local, _ := url.Parse(upstream.URL)
			u.Host = local.Host
			clone.URL = &u
			return upstream.Client().Transport.RoundTrip(clone)
		})}
	}
	before := time.Now().Unix()
	out, err := g.h.Probe(ctx, account, "native-probe-model", "antigravity", "localhost")
	if err != nil || out.Status != 429 || out.Signal != "rate_limit" || out.UpstreamStatus != "RESOURCE_EXHAUSTED" || out.UpstreamReason != "RATE_LIMIT_EXCEEDED" || out.RetryAt < before+29 || out.RetryAt > time.Now().Unix()+31 || !strings.Contains(out.Error, "HTTP 429") {
		t.Fatalf("wrong probe diagnostic: %+v %v", out, err)
	}
	if calls != 1 {
		t.Fatal("probe retried billable request")
	}
	var preview map[string]json.RawMessage
	if json.Unmarshal(out.Request, &preview) != nil || len(preview["request"]) == 0 || len(preview["messages"]) > 0 || len(preview["requestId"]) == 0 || out.Path != "/v1internal:streamGenerateContent" {
		t.Fatal("preview is not the server-built native request")
	}
	public, _ := json.Marshal(out)
	for _, secret := range []string{"private-probe-access", "private-project", "private error message"} {
		if strings.Contains(string(public), secret) {
			t.Fatal("probe exposed upstream private data")
		}
	}
	var charges int
	if err = g.s.DB.QueryRow("SELECT count(*) FROM request_charges").Scan(&charges); err != nil || charges != 0 {
		t.Fatal("probe charged local users", err)
	}
	// A caller using the retired protocol never contacts the provider.
	if _, err = g.h.Probe(ctx, account, "native-probe-model", "messages", "localhost"); err == nil || calls != 1 {
		t.Fatal("Messages probe reached native upstream")
	}
}

func TestGoogleRejectionCodesRemainBoundedAndPrivate(t *testing.T) {
	if got := errorSignal([]byte(`{"error":{"code":429,"status":"RESOURCE_EXHAUSTED"}}`)); got != "rate_limit" {
		t.Fatal("numeric Google code hid status", got)
	}
	status, reason := safeRejectionDetails([]byte(`{"error":{"status":"private-status","message":"private-message","details":[{"@type":"type.googleapis.com/google.rpc.ErrorInfo","reason":"private-reason","metadata":{"token":"private-token"}}]}}`))
	if status != "" || reason != "" {
		t.Fatal("arbitrary provider strings escaped diagnostics")
	}
}
