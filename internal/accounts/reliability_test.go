package accounts

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fiftyapi/internal/credentials"
	"fiftyapi/internal/providers"
	"fiftyapi/internal/store"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestRefreshSurvivesCanceledWaiterAndMergesConcurrentRequests(t *testing.T) {
	ctx := context.Background()
	s, e := store.Open(ctx, filepath.Join(t.TempDir(), "db"))
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	actor, e := s.CreateAdmin(ctx, "admin", "hash")
	if e != nil {
		t.Fatal(e)
	}
	entered := make(chan struct{})
	resume := make(chan struct{})
	var calls atomic.Int64
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/token" {
			t.Error("unexpected refresh endpoint")
			return
		}
		if calls.Add(1) == 1 {
			close(entered)
		}
		<-resume
		io.WriteString(w, `{"access_token":"rotated-access","refresh_token":"rotated-refresh","expires_in":3600}`)
	}))
	defer upstream.Close()
	service, e := New(s, make([]byte, 32), WithHTTPClient(upstream.Client()))
	if e != nil {
		t.Fatal(e)
	}
	cfg := providers.Config{Provider: "openai", ClientID: "client", Scope: "scope", AuthorizeURL: upstream.URL + "/authorize", TokenURL: upstream.URL + "/token", TokenEncoding: "form", RedirectURI: "http://localhost/callback", ModelsURL: upstream.URL + "/models"}
	id, e := service.Import(ctx, actor, "refresh", Credential{Config: cfg, Token: providers.Token{AccessToken: "old-access", RefreshToken: "old-refresh"}, ExpiresAt: time.Now().Add(-time.Minute)})
	if e != nil {
		t.Fatal(e)
	}
	waiter, cancel := context.WithCancel(ctx)
	answer := make(chan error, 1)
	go func() { _, err := service.Access(waiter, id); answer <- err }()
	<-entered
	cancel()
	if e = <-answer; e == nil {
		t.Fatal("canceled waiter did not stop")
	}
	close(resume)
	var wg sync.WaitGroup
	errors := make(chan error, 10)
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			c, e := service.Access(ctx, id)
			if e == nil && (c.Token.AccessToken != "rotated-access" || c.Token.RefreshToken != "rotated-refresh") {
				e = fmt.Errorf("stale credential")
			}
			errors <- e
		}()
	}
	wg.Wait()
	close(errors)
	for e := range errors {
		if e != nil {
			t.Fatal(e)
		}
	}
	if calls.Load() != 1 {
		t.Fatalf("refresh stampede: %d", calls.Load())
	}
	row, e := s.Account(ctx, id)
	if e != nil {
		t.Fatal(e)
	}
	raw, e := credentials.Decrypt(make([]byte, 32), binding(id), row.Credentials)
	if e != nil || !strings.Contains(string(raw), "rotated-refresh") {
		t.Fatal("rotated token was not saved")
	}
}
func TestAuthImportIgnoresEndpointsAndPreservesWorkspace(t *testing.T) {
	ctx := context.Background()
	s, e := store.Open(ctx, filepath.Join(t.TempDir(), "db"))
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	actor, _ := s.CreateAdmin(ctx, "admin", "hash")
	service, e := New(s, make([]byte, 32))
	if e != nil {
		t.Fatal(e)
	}
	claims, _ := json.Marshal(map[string]any{"exp": time.Now().Add(time.Hour).Unix(), "email": "fixture@example.test", "https://api.openai.com/auth": map[string]string{"chatgpt_account_id": "workspace-fixture", "chatgpt_plan_type": "team"}})
	access := "fixture." + base64.RawURLEncoding.EncodeToString(claims) + ".unsigned"
	raw, _ := json.Marshal(map[string]any{"tokens": map[string]any{"access_token": access, "refresh_token": "fixture-refresh", "account_id": "workspace-fixture", "client_id": "app_fixture"}, "config": map[string]string{"models_url": "https://untrusted.example/models"}})
	id, e := service.ImportAuth(ctx, actor, "imported", "openai", raw)
	if e != nil {
		t.Fatal(e)
	}
	stored, e := service.Access(ctx, id)
	if e != nil {
		t.Fatal(e)
	}
	if !strings.HasPrefix(stored.Config.ModelsURL, "https://chatgpt.com/") || stored.Config.OpenAIAccountID != "workspace-fixture" || stored.Config.ClientID != "app_fixture" {
		t.Fatal("import trusted endpoints or lost paired identity")
	}
	info, e := service.CredentialInfo(ctx, id)
	if e != nil || info.Email != "fixture@example.test" || info.Plan != "team" || !info.RefreshAvailable {
		t.Fatal("identity summary missing")
	}
	row, _ := s.Account(ctx, id)
	if row.State != "authorized" || strings.Contains(string(row.Credentials), "fixture-refresh") {
		t.Fatal("unverified import was routable or unencrypted")
	}
}
func TestQuotaQueryValidatesAndPreservesPriorSnapshot(t *testing.T) {
	var requests atomic.Int32
	var body atomic.Value
	body.Store(`{"rate_limit":{"primary_window":{"used_percent":100,"reset_after_seconds":60},"secondary_window":null}}`)
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.URL.Path != "/backend-api/wham/usage" || r.Header.Get("chatgpt-account-id") != "fixture-workspace" {
			t.Error("quota identity/path incorrect")
		}
		io.WriteString(w, body.Load().(string))
	}))
	defer upstream.Close()
	ctx := context.Background()
	s, e := store.Open(ctx, filepath.Join(t.TempDir(), "db"))
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	actor, _ := s.CreateAdmin(ctx, "admin", "hash")
	service, _ := New(s, make([]byte, 32), WithHTTPClient(upstream.Client()))
	cfg := providers.Config{Provider: "openai", OpenAIAccountID: "fixture-workspace", ClientID: "client", Scope: "scope", AuthorizeURL: upstream.URL + "/authorize", TokenURL: upstream.URL + "/token", TokenEncoding: "form", RedirectURI: "http://localhost/callback", ModelsURL: upstream.URL + "/models"}
	id, e := service.Import(ctx, actor, "quota", Credential{Config: cfg, Token: providers.Token{AccessToken: "fixture-access"}, ExpiresAt: time.Now().Add(time.Hour)})
	if e != nil {
		t.Fatal(e)
	}
	health, e := service.QueryOpenAIQuota(ctx, id)
	if e != nil || health.PrimaryUsed == nil || *health.PrimaryUsed != 100 || health.PrimaryReset == nil || len(health.Cooldowns) != 1 {
		t.Fatal("authoritative quota was not persisted")
	}
	// Concurrent page queries share a single upstream request.
	var wg sync.WaitGroup
	before := requests.Load()
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := service.CachedQuota(ctx, id); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if requests.Load() != before+1 {
		t.Fatal("duplicate quota requests were not merged")
	}
	body.Store(`{"rate_limit":{"primary_window":{"used_percent":-10}}}`)
	if _, e = service.QueryOpenAIQuota(ctx, id); e == nil {
		t.Fatal("invalid quota accepted")
	}
	service.quotaQueries.Delete(id)
	before = requests.Load()
	for range 2 {
		if _, err := service.CachedQuota(ctx, id); err == nil {
			t.Fatal("invalid quota should fail")
		}
	}
	if requests.Load() != before+1 {
		t.Fatal("failed quota queries did not back off")
	}
	health, _ = s.AccountHealth(ctx, id)
	if health.PrimaryUsed == nil || *health.PrimaryUsed != 100 {
		t.Fatal("invalid observation erased existing evidence")
	}
}
func TestTemporaryCatalogFailureKeepsKnownGoodMembership(t *testing.T) {
	var fail atomic.Bool
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if fail.Load() {
			w.WriteHeader(503)
			return
		}
		io.WriteString(w, `{"models":[{"slug":"fixture-model"}]}`)
	}))
	defer upstream.Close()
	ctx := context.Background()
	s, e := store.Open(ctx, filepath.Join(t.TempDir(), "db"))
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	actor, _ := s.CreateAdmin(ctx, "admin", "hash")
	service, _ := New(s, make([]byte, 32), WithHTTPClient(upstream.Client()))
	cfg := providers.Config{Provider: "openai", ClientID: "client", Scope: "scope", AuthorizeURL: upstream.URL + "/authorize", TokenURL: upstream.URL + "/token", TokenEncoding: "form", RedirectURI: "http://localhost/callback", ModelsURL: upstream.URL + "/models"}
	id, e := service.Import(ctx, actor, "catalog", Credential{Config: cfg, Token: providers.Token{AccessToken: "fixture-access"}, ExpiresAt: time.Now().Add(time.Hour)})
	if e != nil {
		t.Fatal(e)
	}
	if _, e = service.Verify(ctx, id); e != nil {
		t.Fatal(e)
	}
	before, _ := s.AccountModelsPage(ctx, id, 1, 20, "")
	fail.Store(true)
	if _, e = service.Verify(ctx, id); e == nil {
		t.Fatal("catalog failure concealed")
	}
	row, _ := s.Account(ctx, id)
	after, _ := s.AccountModelsPage(ctx, id, 1, 20, "")
	if row.State != "catalog_ready" || row.LastError == "" || after.Total != 1 || *after.SyncedAt != *before.SyncedAt {
		t.Fatal("temporary discovery failure broke working routing")
	}
}

func TestRejectedRefreshPersistsAfterCallerCancellationAndDrains(t *testing.T) {
	ctx := context.Background()
	s, e := store.Open(ctx, filepath.Join(t.TempDir(), "db"))
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	actor, _ := s.CreateAdmin(ctx, "admin", "hash")
	entered, resume := make(chan struct{}), make(chan struct{})
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(entered)
		<-resume
		io.WriteString(w, `{"access_token":"replacement","refresh_token":"rotated","expires_in":3600}`)
	}))
	defer upstream.Close()
	service, e := New(s, make([]byte, 32), WithHTTPClient(upstream.Client()))
	if e != nil {
		t.Fatal(e)
	}
	cfg := providers.Config{Provider: "openai", ClientID: "client", Scope: "scope", AuthorizeURL: upstream.URL + "/authorize", TokenURL: upstream.URL + "/token", TokenEncoding: "form", RedirectURI: "http://localhost/callback", ModelsURL: upstream.URL + "/models"}
	id, e := service.Import(ctx, actor, "rejected", Credential{Config: cfg, Token: providers.Token{AccessToken: "rejected", RefreshToken: "old"}, ExpiresAt: time.Now().Add(time.Hour)})
	if e != nil {
		t.Fatal(e)
	}
	caller, cancel := context.WithCancel(ctx)
	result := make(chan error, 1)
	go func() { result <- service.RefreshRejected(caller, id, "rejected") }()
	<-entered
	cancel()
	// Shutdown must wait for the critical rotation; a short deadline proves it
	// cannot claim completion while the provider has not returned new credentials.
	short, stop := context.WithTimeout(ctx, 20*time.Millisecond)
	defer stop()
	if service.Shutdown(short) == nil {
		t.Fatal("shutdown skipped active refresh")
	}
	close(resume)
	if e = <-result; e != nil {
		t.Fatal(e)
	}
	if e = service.Shutdown(ctx); e != nil {
		t.Fatal(e)
	}
	row, e := s.Account(ctx, id)
	if e != nil {
		t.Fatal(e)
	}
	plain, e := credentials.Decrypt(make([]byte, 32), binding(id), row.Credentials)
	if e != nil || !strings.Contains(string(plain), "rotated") {
		t.Fatal("rejected-token rotation was lost after cancellation")
	}
	if e = service.RefreshRejected(ctx, id, "replacement"); e == nil {
		t.Fatal("new rotation started after shutdown")
	}
}

func TestMaintenanceDoesNotVerifyUncheckedImport(t *testing.T) {
	ctx := context.Background()
	s, e := store.Open(ctx, filepath.Join(t.TempDir(), "db"))
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	actor, _ := s.CreateAdmin(ctx, "admin", "hash")
	var calls, tokenCalls atomic.Int64
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/token" {
			tokenCalls.Add(1)
			io.WriteString(w, `{"access_token":"refreshed","refresh_token":"refresh-2","expires_in":3600}`)
			return
		}
		calls.Add(1)
		io.WriteString(w, `{"models":[{"slug":"fixture-model"}]}`)
	}))
	defer upstream.Close()
	service, e := New(s, make([]byte, 32), WithHTTPClient(upstream.Client()))
	if e != nil {
		t.Fatal(e)
	}
	cfg := providers.Config{Provider: "openai", ClientID: "client", Scope: "scope", AuthorizeURL: upstream.URL + "/authorize", TokenURL: upstream.URL + "/token", TokenEncoding: "form", RedirectURI: "http://localhost/callback", ModelsURL: upstream.URL + "/models"}
	id, e := service.Import(ctx, actor, "unchecked", Credential{Config: cfg, Token: providers.Token{AccessToken: "fixture", RefreshToken: "refresh-1"}, ExpiresAt: time.Now().Add(time.Hour)})
	if e != nil {
		t.Fatal(e)
	}
	service.Maintain(ctx)
	if calls.Load() != 0 {
		t.Fatal("unchecked import contacted provider during maintenance")
	}
	row, e := s.Account(ctx, id)
	if e != nil || row.State != "authorized" {
		t.Fatal("unchecked import state changed")
	}
	if e = s.SyncModels(ctx, id, "openai", []string{"fixture-model"}); e != nil {
		t.Fatal(e)
	}
	if e = s.AccountState(ctx, id, "catalog_ready", ""); e != nil {
		t.Fatal(e)
	}
	service.Maintain(ctx)
	if calls.Load() != 1 {
		t.Fatal("verified account stopped receiving maintenance")
	}
	// Routine maintenance within the daily window refreshes expiring tokens
	// without re-pulling the model catalog.
	service.checked.Store(id, time.Now().Add(-10*time.Minute))
	service.Maintain(ctx)
	if calls.Load() != 1 || tokenCalls.Load() != 0 {
		t.Fatal("healthy account resynced catalog or refreshed non-expiring token inside daily window")
	}
	expiring, e := service.seal(id, Credential{Config: cfg, Token: providers.Token{AccessToken: "fixture", RefreshToken: "refresh-1"}, ExpiresAt: time.Now().Add(time.Minute)})
	if e != nil || s.UpdateAccountCredentials(ctx, id, expiring) != nil {
		t.Fatal("failed to stage expiring token")
	}
	service.Maintain(ctx)
	if tokenCalls.Load() != 1 || calls.Load() != 1 {
		t.Fatalf("expected token-only maintenance refresh, got token=%d catalog=%d", tokenCalls.Load(), calls.Load())
	}
	service.checked.Store(id, time.Now().Add(-catalogSyncInterval-time.Minute))
	service.Maintain(ctx)
	if calls.Load() != 2 {
		t.Fatal("daily catalog sync did not run after interval elapsed")
	}
}

func TestConnectionSaveCannotOverwriteSchedulingConcurrency(t *testing.T) {
	ctx := context.Background()
	s, e := store.Open(ctx, filepath.Join(t.TempDir(), "db"))
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	actor, _ := s.CreateAdmin(ctx, "admin", "hash")
	service, e := New(s, make([]byte, 32))
	if e != nil {
		t.Fatal(e)
	}
	credential := testCredential()
	credential.Config.MaxConcurrency = 7
	id, e := service.Import(ctx, actor, "legacy-concurrency", credential)
	if e != nil {
		t.Fatal(e)
	}
	stale, e := service.RuntimeConfig(ctx, id)
	if e != nil || stale.MaxConcurrency != 7 {
		t.Fatal("legacy concurrency was not initialized")
	}
	policy := store.DefaultAccountPolicy()
	policy.Concurrency = 9
	policy.Priority = 13
	policy.Weight = 3
	policy.MaxQueue = 17
	policy.WaitMS = 800
	if e = s.SetAccountPolicy(ctx, actor, id, policy); e != nil {
		t.Fatal(e)
	}
	for _, legacy := range []int{stale.MaxConcurrency, 0, 1000} {
		stale.MaxConcurrency = legacy
		stale.TimeoutSeconds = 30
		if e = service.UpdateRuntime(ctx, actor, id, stale); e != nil {
			t.Fatal(e)
		}
		current, e := s.AccountPolicy(ctx, id)
		if e != nil || current != policy {
			t.Fatal("connection save overwrote scheduling policy")
		}
	}
	// Reopening a service must not migrate the old encrypted value over policy.
	service, e = New(s, make([]byte, 32))
	if e != nil {
		t.Fatal(e)
	}
	current, e := service.RuntimeConfig(ctx, id)
	if e != nil || current.MaxConcurrency != 9 || current.TimeoutSeconds != 30 {
		t.Fatal("connection changes or scheduling did not survive service reopen")
	}
}
