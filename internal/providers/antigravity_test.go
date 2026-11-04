package providers

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func noWait(ctx context.Context, _ time.Duration) error { return ctx.Err() }

func TestAntigravityOnboard(t *testing.T) {
	for _, tc := range []struct {
		name, load, onboard string
		wantCalls           int
		wantError           bool
	}{
		{"success", `{"allowedTiers":[{"id":"skip","isDefault":false},{"id":"default-tier","isDefault":true}]}`, `{"done":true,"response":{"cloudaicompanionProject":{"id":"new-project"}}}`, 1, false},
		{"no-default", `{"allowedTiers":[{"id":"other"}]}`, `{}`, 0, true},
		{"existing", `{"cloudaicompanionProject":"new-project"}`, `{}`, 0, false},
		{"no-project", `{"allowedTiers":[{"id":"default-tier","isDefault":true}]}`, `{"done":true,"response":{}}`, 4, true},
		{"malformed", `{"allowedTiers":[{"id":"default-tier","isDefault":true}]}`, `{}`, 4, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls, listed := 0, false
			s := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/v1internal:loadCodeAssist":
					io.WriteString(w, tc.load)
				case "/v1internal:onboardUser":
					calls++
					if r.Header.Get("Authorization") != "Bearer access" {
						t.Error("missing bearer")
					}
					var input struct {
						Tier     string            `json:"tierId"`
						Metadata map[string]string `json:"metadata"`
					}
					if json.NewDecoder(r.Body).Decode(&input) != nil || input.Tier != "default-tier" || input.Metadata["ideType"] != "ANTIGRAVITY" {
						t.Error("wrong onboarding request")
					}
					io.WriteString(w, tc.onboard)
				case "/v1internal:fetchAvailableModels":
					listed = true
					var body map[string]string
					if json.NewDecoder(r.Body).Decode(&body) != nil || body["project"] != "new-project" {
						t.Error("wrong project")
					}
					io.WriteString(w, `{"models":{}}`)
				default:
					t.Error("unexpected path")
					w.WriteHeader(404)
				}
			}))
			defer s.Close()
			c := config()
			c.Provider = "antigravity"
			c.AllowOnboard = true
			c.ModelsURL = s.URL + "/v1internal:fetchAvailableModels"
			c.ModelsMethod = "POST"
			c.ModelsBody = json.RawMessage(`{}`)
			resolved, err := resolveAntigravityProjectWithWait(context.Background(), s.Client(), c, Token{AccessToken: "access"}, noWait)
			if err == nil {
				_, err = Models(context.Background(), s.Client(), resolved, Token{AccessToken: "access"})
			}
			if (err != nil) != tc.wantError || calls != tc.wantCalls || listed == tc.wantError {
				t.Fatalf("error=%v calls=%d listed=%v", err, calls, listed)
			}
		})
	}
}

func TestAntigravityProjectDiscovery(t *testing.T) {
	for _, tc := range []struct {
		name, response, body string
		wantError            bool
		wantDiscovery        bool
	}{
		{"string", `{"cloudaicompanionProject":"my-project"}`, `{}`, false, true},
		{"object", `{"cloudaicompanionProject":{"id":"my-project"}}`, `{}`, false, true},
		{"legacy-placeholder", `{"cloudaicompanionProject":"my-project"}`, `{"project":"REPLACE_WITH_PROJECT_ID"}`, false, true},
		{"missing", `{}`, `{}`, true, true},
		{"missing-placeholder", `{}`, `{"project":"REPLACE_WITH_PROJECT_ID"}`, true, true},
		{"explicit", `{}`, `{"project":"my-project"}`, false, false},
		{"error", `{"error":{"message":"private"}}`, `{}`, true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			discovered, listed := false, false
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Authorization") != "Bearer access" {
					t.Error("missing authentication")
				}
				switch r.URL.Path {
				case "/v1internal:loadCodeAssist":
					discovered = true
					if r.Method != "POST" {
						t.Error("wrong method")
					}
					var input struct {
						Metadata map[string]string `json:"metadata"`
					}
					if json.NewDecoder(r.Body).Decode(&input) != nil || input.Metadata["ideType"] != "ANTIGRAVITY" {
						t.Error("bad metadata")
					}
					io.WriteString(w, tc.response)
				case "/v1internal:fetchAvailableModels":
					listed = true
					var input map[string]string
					if json.NewDecoder(r.Body).Decode(&input) != nil || input["project"] != "my-project" {
						t.Error("wrong project")
					}
					io.WriteString(w, `{"models":{"model-a":{}}}`)
				default:
					t.Error("unexpected endpoint: " + r.URL.Path)
					w.WriteHeader(404)
				}
			}))
			defer server.Close()
			c := config()
			c.Provider = "antigravity"
			c.ModelsURL = server.URL + "/v1internal:fetchAvailableModels"
			c.ModelsMethod = "POST"
			c.ModelsBody = json.RawMessage(tc.body)
			resolved, err := resolveAntigravityProjectWithWait(context.Background(), server.Client(), c, Token{AccessToken: "access"}, noWait)
			var ids []string
			if err == nil {
				ids, err = Models(context.Background(), server.Client(), resolved, Token{AccessToken: "access"})
			}
			if (err != nil) != tc.wantError {
				t.Fatalf("unexpected error: %v", err)
			}
			if discovered != tc.wantDiscovery {
				t.Fatal("wrong discovery behavior")
			}
			if tc.wantError {
				if listed {
					t.Fatal("listed without project")
				}
				if strings.Contains(err.Error(), "private") {
					t.Fatal("leaked response")
				}
			} else if !listed || len(ids) != 1 {
				t.Fatal("catalog not fetched")
			}
		})
	}
}

func TestAntigravityRechecksProjectAfterOnboard(t *testing.T) {
	loads, onboards := 0, 0
	var delays []time.Duration
	s := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("User-Agent") != "antigravity/hub/9.8.7 windows/amd64" {
			t.Error("incorrect UA")
		}
		switch r.URL.Path {
		case "/v1internal:loadCodeAssist":
			loads++
			var request struct {
				Metadata map[string]string `json:"metadata"`
			}
			if json.NewDecoder(r.Body).Decode(&request) != nil || request.Metadata["ideVersion"] != "9.8.7" {
				t.Error("version metadata missing")
			}
			if loads == 1 {
				io.WriteString(w, `{"allowedTiers":[{"id":"default","isDefault":true}]}`)
			} else {
				io.WriteString(w, `{"cloudaicompanionProject":"late-project"}`)
			}
		case "/v1internal:onboardUser":
			onboards++
			io.WriteString(w, `{"done":true,"response":{}}`)
		case "/v1internal:fetchAvailableModels":
			var request map[string]string
			if json.NewDecoder(r.Body).Decode(&request) != nil || request["project"] != "late-project" {
				t.Error("missing discovered project")
			}
			io.WriteString(w, `{"models":{"ready-model":{}}}`)
		default:
			t.Error("unexpected path")
		}
	}))
	defer s.Close()
	c := config()
	c.Provider = "antigravity"
	c.AllowOnboard = true
	c.AntigravityVersion = "9.8.7"
	c.ModelsURL = s.URL + "/v1internal:fetchAvailableModels"
	resolved, err := resolveAntigravityProjectWithWait(context.Background(), s.Client(), c, Token{AccessToken: "access"}, func(ctx context.Context, d time.Duration) error { delays = append(delays, d); return ctx.Err() })
	if err != nil {
		t.Fatal(err)
	}
	ids, err := Models(context.Background(), s.Client(), resolved, Token{AccessToken: "access"})
	if err != nil || len(ids) != 1 || loads != 2 || onboards != 1 || len(delays) != 1 || delays[0] != time.Second {
		t.Fatalf("recovery failed: %v loads=%d onboards=%d delays=%v", err, loads, onboards, delays)
	}
}

func TestAntigravityPendingAndCancellation(t *testing.T) {
	loads := 0
	s := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { loads++; io.WriteString(w, `{}`) }))
	defer s.Close()
	c := config()
	c.Provider = "antigravity"
	c.ModelsURL = s.URL + "/v1internal:fetchAvailableModels"
	var delays []time.Duration
	_, err := resolveAntigravityProjectWithWait(context.Background(), s.Client(), c, Token{AccessToken: "access"}, func(ctx context.Context, d time.Duration) error { delays = append(delays, d); return ctx.Err() })
	var pending *ProjectPendingError
	if !errors.As(err, &pending) || pending.Attempts != 4 || loads != 4 || len(delays) != 3 || delays[0] != time.Second || delays[1] != 2*time.Second || delays[2] != 4*time.Second {
		t.Fatalf("wrong retry state: %v %v", err, delays)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = resolveAntigravityProjectWithWait(ctx, s.Client(), c, Token{}, noWait)
	if !errors.Is(err, context.Canceled) || loads != 4 {
		t.Fatal("cancellation not honored")
	}
}

func TestAntigravityEndpointFallback(t *testing.T) {
	for _, status := range []int{401, 403, 404, 408, 429, 500} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			fallbackCalls := 0
			fallback := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				fallbackCalls++
				if r.Header.Get("Authorization") != "Bearer access" {
					t.Error("missing bearer")
				}
				io.WriteString(w, `{"models":{}}`)
			}))
			defer fallback.Close()
			primary := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(status)
				io.WriteString(w, `{"error":"private-value"}`)
			}))
			defer primary.Close()
			c := config()
			c.Provider = "antigravity"
			c.ModelsURL = primary.URL + "/v1internal:fetchAvailableModels"
			c.AntigravityFallbackURLs = []string{fallback.URL}
			c.ModelsBody = json.RawMessage(`{"project":"existing"}`)
			_, err := Models(context.Background(), primary.Client(), c, Token{AccessToken: "access"})
			wantFallback := status != 401 && status != 403
			if wantFallback && (err != nil || fallbackCalls != 1) {
				t.Fatalf("no fallback: %v calls=%d", err, fallbackCalls)
			}
			if !wantFallback && (err == nil || fallbackCalls != 0) {
				t.Fatalf("authentication error switched endpoint: %v calls=%d", err, fallbackCalls)
			}
		})
	}
}
