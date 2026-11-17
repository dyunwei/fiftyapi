package pricing

import (
	"context"
	"fiftyapi/internal/store"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
)

type fixtureTransport func(*http.Request) (*http.Response, error)

func (f fixtureTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func TestStableReleaseIsCandidateOnly(t *testing.T) {
	ctx := context.Background()
	s, err := store.Open(ctx, filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	service := New(s)
	prerelease := false
	service.Client = &http.Client{Transport: fixtureTransport(func(r *http.Request) (*http.Response, error) {
		if r.URL.String() != CodexReleaseURL {
			t.Fatal("unexpected request")
		}
		flag := "false"
		if prerelease {
			flag = "true"
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"tag_name":"rust-v0.200.0","html_url":"https://github.com/openai/codex/releases/tag/rust-v0.200.0","prerelease":` + flag + `}`)), Header: make(http.Header)}, nil
	})}
	if err = service.CheckCodex(ctx); err != nil {
		t.Fatal(err)
	}
	candidate, ok, err := s.Setting(ctx, "codex_candidate")
	if err != nil || !ok || !strings.Contains(candidate, "0.200.0") {
		t.Fatal("candidate not saved", err)
	}
	if _, ok, err = s.Setting(ctx, "client_identity"); err != nil || ok {
		t.Fatal("release discovery activated a version")
	}
	prerelease = true
	if service.CheckCodex(ctx) == nil {
		t.Fatal("prerelease treated as stable")
	}
	after, _, _ := s.Setting(ctx, "codex_candidate")
	if after != candidate {
		t.Fatal("invalid release destroyed previous candidate")
	}
}
