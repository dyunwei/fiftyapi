package server

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"io"
	"net"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"fiftyapi/internal/accounts"
	"fiftyapi/internal/store"
)

func startMockSOCKS5(t *testing.T, wantUser, wantPass string) (string, int) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				_ = c.SetDeadline(time.Now().Add(2 * time.Second))
				var hdr [2]byte
				if _, err := io.ReadFull(c, hdr[:]); err != nil || hdr[0] != 0x05 {
					return
				}
				methods := make([]byte, int(hdr[1]))
				if _, err := io.ReadFull(c, methods); err != nil {
					return
				}
				if wantUser == "" && wantPass == "" {
					_, _ = c.Write([]byte{0x05, 0x00})
					return
				}
				hasUserPass := false
				for _, m := range methods {
					if m == 0x02 {
						hasUserPass = true
					}
				}
				if !hasUserPass {
					_, _ = c.Write([]byte{0x05, 0xFF})
					return
				}
				_, _ = c.Write([]byte{0x05, 0x02})
				var ver [2]byte
				if _, err := io.ReadFull(c, ver[:]); err != nil || ver[0] != 0x01 {
					return
				}
				u := make([]byte, int(ver[1]))
				if _, err := io.ReadFull(c, u); err != nil {
					return
				}
				var plen [1]byte
				if _, err := io.ReadFull(c, plen[:]); err != nil {
					return
				}
				p := make([]byte, int(plen[0]))
				if _, err := io.ReadFull(c, p); err != nil {
					return
				}
				if string(u) == wantUser && string(p) == wantPass {
					_, _ = c.Write([]byte{0x01, 0x00})
				} else {
					_, _ = c.Write([]byte{0x01, 0x01})
				}
			}(conn)
		}
	}()
	addr := ln.Addr().(*net.TCPAddr)
	return addr.IP.String(), addr.Port
}

func TestProxyCRUDHealthCheckAccountBindingAndContributionSelection(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "db")
	s, err := store.Open(ctx, dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	master := make([]byte, 32)
	a, err := New(s, Config{SettingsKey: master})
	if err != nil {
		t.Fatal(err)
	}
	svc, err := accounts.New(s, master)
	if err != nil {
		t.Fatal(err)
	}
	a.AttachAccounts(svc)
	adminID, err := s.CreateAdmin(ctx, "admin", "hash")
	if err != nil {
		t.Fatal(err)
	}
	session := strings.Repeat("p", 43)
	csrf := "csrf-token"
	sessionHash := sha256.Sum256([]byte(session))
	if err = s.SaveSession(ctx, sessionHash[:], adminID, csrf, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	handler := a.Handler()

	do := func(method, path, body string, authed bool) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, "http://example.test"+path, strings.NewReader(body))
		if body != "" {
			req.Header.Set("Content-Type", "application/json")
		}
		req.Header.Set("Origin", "http://example.test")
		if authed {
			req.Header.Set("Cookie", cookieName+"="+session)
			req.Header.Set("X-CSRF-Token", csrf)
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		return w
	}

	host, port := startMockSOCKS5(t, "proxyuser", "secret-pass")

	// Create SOCKS5 proxy with valid credentials -> auto health check succeeds.
	createBody := `{"protocol":"socks5","host":"` + host + `","port":` + strconv.Itoa(port) + `,"location":"美国 · 洛杉矶","note":"主力节点","username":"proxyuser","password":"secret-pass"}`
	w := do("POST", "/api/admin/proxies", createBody, true)
	if w.Code != 201 {
		t.Fatalf("create proxy failed: %d %s", w.Code, w.Body.String())
	}
	if strings.Contains(w.Body.String(), "secret-pass") {
		t.Fatal("proxy response leaked password")
	}
	var created store.ProxyServer
	if err := json.Unmarshal(w.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	if !created.HasPassword || created.Healthy == nil || !*created.Healthy || created.Location != "美国 · 洛杉矶" {
		t.Fatalf("unexpected created proxy: %+v", created)
	}

	// Update proxy with wrong password -> health check marks unhealthy.
	updateWrong := `{"protocol":"socks5","host":"` + host + `","port":` + strconv.Itoa(port) + `,"location":"美国 · 洛杉矶","note":"节点更新","username":"proxyuser","password":"wrong-pass"}`
	w = do("PUT", "/api/admin/proxies/"+strconv.FormatInt(created.ID, 10), updateWrong, true)
	if w.Code != 200 {
		t.Fatalf("update proxy failed: %d %s", w.Code, w.Body.String())
	}
	var updated store.ProxyServer
	_ = json.Unmarshal(w.Body.Bytes(), &updated)
	if updated.Healthy == nil || *updated.Healthy || updated.LastError == "" {
		t.Fatalf("expected unhealthy proxy on wrong auth: %+v", updated)
	}

	// Update back to valid password, then update note while keeping "***" masked password.
	FixBody := `{"protocol":"socks5","host":"` + host + `","port":` + strconv.Itoa(port) + `,"location":"美国 · 洛杉矶","note":"主力节点","username":"proxyuser","password":"secret-pass"}`
	_ = do("PUT", "/api/admin/proxies/"+strconv.FormatInt(created.ID, 10), FixBody, true)
	keepMasked := `{"protocol":"socks5","host":"` + host + `","port":` + strconv.Itoa(port) + `,"location":"日本 · 东京","note":"保留密码","username":"proxyuser","password":"***"}`
	w = do("PUT", "/api/admin/proxies/"+strconv.FormatInt(created.ID, 10), keepMasked, true)
	if w.Code != 200 {
		t.Fatalf("update masked proxy failed: %d %s", w.Code, w.Body.String())
	}
	_ = json.Unmarshal(w.Body.Bytes(), &updated)
	if !updated.HasPassword || updated.Healthy == nil || !*updated.Healthy || updated.Location != "日本 · 东京" {
		t.Fatalf("masked password lost or health check failed: %+v", updated)
	}

	// Create contribution link with default proxy_id.
	w = do("POST", "/api/admin/contribution-links", `{"provider":"openai","hours":24,"note":"研发分享","proxy_id":`+strconv.FormatInt(created.ID, 10)+`}`, true)
	if w.Code != 201 {
		t.Fatalf("create contribution link failed: %d %s", w.Code, w.Body.String())
	}
	var linkResp struct {
		ID       int64  `json:"id"`
		Fragment string `json:"fragment"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &linkResp)
	token := strings.TrimPrefix(linkResp.Fragment, "#contribute=")

	// Public check endpoint returns sanitized proxy list without host/port/username/password.
	w = do("POST", "/api/contributions/check", `{"token":"`+token+`"}`, false)
	if w.Code != 200 {
		t.Fatalf("check link failed: %d %s", w.Code, w.Body.String())
	}
	checkRaw := w.Body.String()
	if strings.Contains(checkRaw, host) || strings.Contains(checkRaw, strconv.Itoa(port)) || strings.Contains(checkRaw, "proxyuser") || strings.Contains(checkRaw, "secret-pass") {
		t.Fatalf("public check endpoint leaked proxy host/port/credentials: %s", checkRaw)
	}
	if !strings.Contains(checkRaw, "日本 · 东京") || !strings.Contains(checkRaw, "保留密码") {
		t.Fatalf("public check endpoint missing location/note: %s", checkRaw)
	}

	// Contribute an account choosing the proxy.
	submit := `{"token":"` + token + `","proxy_id":` + strconv.FormatInt(created.ID, 10) + `,"accounts":[{"name":"contributed-with-proxy","auth":{"refresh_token":"contrib-refresh-token"}}]}`
	w = do("POST", "/api/contributions/import", submit, false)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"imported":1`) {
		t.Fatalf("contribute account failed: %d %s", w.Code, w.Body.String())
	}

	accountsList, err := s.Accounts(ctx)
	if err != nil || len(accountsList) != 1 || accountsList[0].ProxyID == nil || *accountsList[0].ProxyID != created.ID {
		t.Fatalf("contributed account did not bind proxy_id: %+v", accountsList)
	}
	accID := accountsList[0].ID

	// Deleting proxy while bound to an active account must be rejected.
	w = do("DELETE", "/api/admin/proxies/"+strconv.FormatInt(created.ID, 10), "", true)
	if w.Code != 400 || !strings.Contains(w.Body.String(), "解绑") {
		t.Fatalf("expected delete protection when proxy is in use: %d %s", w.Code, w.Body.String())
	}

	// Unbind proxy from account via RuntimeConfig update (proxy_id: 0).
	cfg, err := svc.RuntimeConfig(ctx, accID)
	if err != nil || cfg.ProxyID != created.ID {
		t.Fatalf("unexpected runtime config proxy_id: %+v %v", cfg, err)
	}
	cfg.ProxyID = 0
	if err := svc.UpdateRuntime(ctx, adminID, accID, cfg); err != nil {
		t.Fatal(err)
	}
	cfgAfter, err := svc.RuntimeConfig(ctx, accID)
	if err != nil || cfgAfter.ProxyID != 0 || cfgAfter.ProxyURL != "" {
		t.Fatalf("proxy not unbound: %+v %v", cfgAfter, err)
	}

	// Now deleting the proxy succeeds.
	w = do("DELETE", "/api/admin/proxies/"+strconv.FormatInt(created.ID, 10), "", true)
	if w.Code != 200 {
		t.Fatalf("delete unbound proxy failed: %d %s", w.Code, w.Body.String())
	}
}
