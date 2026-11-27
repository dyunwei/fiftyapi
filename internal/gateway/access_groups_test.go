package gateway

import (
	"context"
	"encoding/json"
	"fiftyapi/internal/store"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
)

func TestAccessGroupScopesGenerationAndCodexManifest(t *testing.T) {
	ctx := context.Background()
	var privateCalls, sharedCalls atomic.Int64
	var failShared atomic.Bool
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		private := r.Header.Get("Authorization") == "Bearer upstream-private-token"
		if private {
			privateCalls.Add(1)
		} else {
			sharedCalls.Add(1)
		}
		if r.URL.Path == "/models" {
			_, _ = io.WriteString(w, `{"models":[{"slug":"model"},{"slug":"private-model"}]}`)
			return
		}
		if !private && failShared.Load() {
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = io.WriteString(w, `{"error":{"message":"Overloaded"}}`)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, schedulerSuccess)
	}))
	defer upstream.Close()
	g := setupGateway(t, "openai", upstream)
	addPoolAccount(t, g)
	group, err := g.s.SetAccessGroup(ctx, g.user, 0, "Engineering")
	if err != nil {
		t.Fatal(err)
	}
	if err = g.s.SetUserAccess(ctx, g.user, g.user, store.UserAccess{GroupID: &group.ID}); err != nil {
		t.Fatal(err)
	}
	if err = g.s.SetAccountAccess(ctx, g.user, g.account, store.AccountAccess{Mode: "groups", GroupIDs: []int64{group.ID}}); err != nil {
		t.Fatal(err)
	}
	if err = g.s.SyncModels(ctx, g.account, "openai", []string{"model", "private-model"}); err != nil {
		t.Fatal(err)
	}
	if err = g.s.SetModel(ctx, g.user, "private-model", true, &store.Price{Input: 1000000, Output: 1000000}); err != nil {
		t.Fatal(err)
	}

	manifest := func(path, collection, slug string) []string {
		t.Helper()
		r := httptest.NewRequest(http.MethodGet, path, nil)
		r.Header.Set("Authorization", "Bearer "+g.token)
		w := httptest.NewRecorder()
		g.h.Models(w, r)
		var envelope map[string]json.RawMessage
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &envelope) != nil {
			t.Fatalf("catalog response: %d %s", w.Code, w.Body)
		}
		var entries []map[string]json.RawMessage
		if err := json.Unmarshal(envelope[collection], &entries); err != nil {
			t.Fatal(err)
		}
		ids := []string{}
		for _, entry := range entries {
			var value string
			if err := json.Unmarshal(entry[slug], &value); err != nil {
				t.Fatal(err)
			}
			ids = append(ids, value)
		}
		return ids
	}
	manifestPath := "/v1/models?client_version=0.164.0"
	if models := manifest(manifestPath, "models", "slug"); !slices.Contains(models, "private-model") {
		t.Fatalf("authorized manifest: %v", models)
	}
	if w := poolRequest(g, "bound-group", `{"model":"model","input":"before revocation"}`); w.Code != 200 || privateCalls.Load() != 2 {
		t.Fatalf("authorized routing: %d %s (%d private calls)", w.Code, w.Body, privateCalls.Load())
	}
	zero := int64(0)
	if err = g.s.SetUserAccess(ctx, g.user, g.user, store.UserAccess{GroupID: &zero}); err != nil {
		t.Fatal(err)
	}
	for _, item := range []struct{ path, collection, slug string }{
		{"/v1/models", "data", "id"}, {manifestPath, "models", "slug"},
	} {
		if models := manifest(item.path, item.collection, item.slug); !slices.Equal(models, []string{"model"}) {
			t.Fatalf("catalog after revocation: %v", models)
		}
	}
	if w := poolRequest(g, "bound-group", `{"model":"model","input":"portable continuation"}`); w.Code != 200 {
		t.Fatalf("portable continuation could not move to shared account: %d %s", w.Code, w.Body)
	}
	if w := poolRequest(g, "private-only", `{"model":"private-model","input":"denied"}`); w.Code != 403 || !strings.Contains(w.Body.String(), store.ErrAccountAccess.Error()) {
		t.Fatalf("private model request: %d %s", w.Code, w.Body)
	}
	failShared.Store(true)
	if w := poolRequest(g, "new-group-session", `{"model":"model","input":"retry"}`); w.Code == 200 {
		t.Fatal("request unexpectedly succeeded when the only permitted route failed")
	}
	if privateCalls.Load() != 2 || sharedCalls.Load() < 3 {
		t.Fatalf("incorrect upstream dispatch counts: private=%d shared=%d", privateCalls.Load(), sharedCalls.Load())
	}
}
