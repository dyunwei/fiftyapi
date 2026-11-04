package server

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fiftyapi/internal/backup"
	"fiftyapi/internal/buildinfo"
	"fiftyapi/internal/i18n"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"fiftyapi/internal/accounts"
	"fiftyapi/internal/gateway"
	"fiftyapi/internal/mailer"
	"fiftyapi/internal/store"
	"golang.org/x/crypto/bcrypt"
)

type Config struct {
	SettingsKey   []byte
	Mailer        mailer.Sender
	PublicURL     string
	SetupToken    string
	SecureCookies bool
}
type API struct {
	backups   *backup.Service
	store     *store.Store
	cfg       Config
	dummyHash []byte
	mu        sync.Mutex
	emailMu   sync.Mutex
	attempts  map[string]attempt
	accounts  *accounts.Service
	gateway   *gateway.Handler
}
type attempt struct {
	count int
	until time.Time
}
type identity struct {
	user store.User
	csrf string
	hash []byte
}

// Browser session cookie for FiftyAPI.
const cookieName = "fiftyapi_session"

var usernamePattern = regexp.MustCompile(`^[A-Za-z0-9_.-]{3,64}$`)

func New(s *store.Store, cfg Config) (*API, error) {
	if len(cfg.SettingsKey) != 0 && len(cfg.SettingsKey) != 32 {
		return nil, errors.New("设置加密密钥需为 32 字节")
	}
	cfg.SettingsKey = append([]byte(nil), cfg.SettingsKey...)
	if cfg.Mailer != nil && !validPublicURL(cfg.PublicURL) {
		return nil, errors.New("邀请邮件需要有效的 FIFTYAPI_PUBLIC_URL：HTTP 或 HTTPS 源站地址")
	}
	dummy, err := bcrypt.GenerateFromPassword([]byte("unused-dummy-password"), 12)
	if err != nil {
		return nil, err
	}
	return &API{backups: backup.New(s, cfg.SettingsKey), store: s, cfg: cfg, dummyHash: dummy, attempts: map[string]attempt{}}, nil
}
func (a *API) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		if err := a.store.DB.PingContext(r.Context()); err != nil {
			writeError(w, 503, "数据库不可用")
			return
		}
		writeJSON(w, 200, map[string]string{"status": "ok"})
	})
	mux.HandleFunc("GET /api/version", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, map[string]string{"version": buildinfo.Version(), "commit": buildinfo.Commit})
	})
	mux.HandleFunc("GET /api/setup/status", a.setupStatus)
	mux.HandleFunc("POST /api/setup", a.setup)
	mux.HandleFunc("POST /api/auth/login", a.login)
	mux.HandleFunc("POST /api/auth/register", a.register)
	mux.HandleFunc("POST /api/invitations/check", a.checkInvitation)
	mux.HandleFunc("POST /api/invitations/accept", a.acceptInvitation)
	mux.HandleFunc("GET /api/admin/invitations/config", a.protect(true, false, a.invitationConfig))
	mux.HandleFunc("POST /api/admin/invitations", a.protect(true, true, a.inviteUser))
	mux.HandleFunc("POST /api/admin/users/{id}/invitation", a.protect(true, true, a.resendInvitation))
	mux.HandleFunc("GET /api/me", a.protect(false, false, a.me))
	mux.HandleFunc("POST /api/auth/logout", a.protect(false, true, a.logout))
	mux.HandleFunc("GET /api/admin/users", a.protect(true, false, a.users))
	mux.HandleFunc("POST /api/admin/users", a.protect(true, true, a.createUser))
	mux.HandleFunc("PUT /api/admin/users/{id}/enabled", a.protect(true, true, a.enableUser))
	mux.HandleFunc("POST /api/admin/gateway-accounts", a.protect(true, true, a.createGatewayAccount))
	mux.HandleFunc("GET /api/admin/contribution-links", a.protect(true, false, a.listContributionLinks))
	mux.HandleFunc("POST /api/admin/contribution-links", a.protect(true, true, a.createContributionLink))
	mux.HandleFunc("POST /api/admin/contribution-links/{id}/revoke", a.protect(true, true, a.revokeContributionLink))
	mux.HandleFunc("POST /api/contributions/check", a.checkContributionLink)
	mux.HandleFunc("POST /api/contributions/import", a.contributeAccounts)
	mux.HandleFunc("GET /api/admin/accounts", a.protect(true, false, a.listAccounts))
	mux.HandleFunc("POST /api/admin/accounts/{id}/models/test", a.protect(true, true, a.testAccountModel))
	mux.HandleFunc("GET /api/admin/accounts/{id}/models", a.protect(true, false, a.accountModels))
	mux.HandleFunc("POST /api/admin/accounts/import", a.protect(true, true, a.importAccounts))
	mux.HandleFunc("POST /api/admin/accounts/{id}/quota/query", a.protect(true, true, a.queryAccountQuota))
	mux.HandleFunc("GET /api/admin/accounts/{id}/routing", a.protect(true, false, a.accountRouting))
	mux.HandleFunc("PUT /api/admin/accounts/{id}/routing", a.protect(true, true, a.setAccountRouting))
	mux.HandleFunc("POST /api/admin/accounts/{id}/routing/reset", a.protect(true, true, a.resetAccountRouting))
	mux.HandleFunc("PUT /api/admin/accounts/{id}/name", a.protect(true, true, a.renameAccount))
	mux.HandleFunc("DELETE /api/admin/accounts/{id}", a.protect(true, true, a.deleteAccount))
	mux.HandleFunc("GET /api/admin/logs/{id}/attempts", a.protect(true, false, a.requestAttempts))
	mux.HandleFunc("GET /api/logs/{id}/attempts", a.protect(false, false, a.requestAttempts))

	mux.HandleFunc("PUT /api/admin/accounts/{id}/enabled", a.protect(true, true, a.enableAccount))
	mux.HandleFunc("POST /api/admin/accounts/{id}/verify", a.protect(true, true, a.verifyAccount))
	mux.HandleFunc("PUT /api/me/password", a.protect(false, true, a.changePassword))
	mux.HandleFunc("PUT /api/admin/users/{id}/password", a.protect(true, true, a.resetPassword))
	mux.HandleFunc("PUT /api/admin/users/{id}/quota", a.protect(true, true, a.setLimits))
	mux.HandleFunc("POST /api/admin/users/{id}/quota/reset", a.protect(true, true, a.resetQuota))
	mux.HandleFunc("GET /api/quota", a.protect(false, false, a.quota))
	mux.HandleFunc("GET /api/dashboard", a.protect(false, false, a.dashboard))
	mux.HandleFunc("GET /api/keys", a.protect(false, false, a.keys))
	mux.HandleFunc("POST /api/keys", a.protect(false, true, a.createKey))
	mux.HandleFunc("POST /api/keys/{id}/revoke", a.protect(false, true, a.revokeKey))
	mux.HandleFunc("DELETE /api/keys/{id}", a.protect(false, true, a.deleteKey))
	mux.HandleFunc("GET /api/models", a.protect(false, false, a.models))
	mux.HandleFunc("GET /api/admin/models", a.protect(true, false, a.models))
	mux.HandleFunc("PUT /api/admin/models", a.protect(true, true, a.setModel))
	mux.HandleFunc("GET /api/logs", a.protect(false, false, a.logs))
	mux.HandleFunc("GET /api/admin/logs", a.protect(true, false, a.logs))
	mux.HandleFunc("GET /api/admin/logs/{id}/settlement", a.protect(true, false, a.settlementDetails))
	mux.HandleFunc("POST /api/admin/logs/{id}/retry-settlement", a.protect(true, true, a.retrySettlement))
	mux.HandleFunc("POST /api/admin/logs/{id}/settle", a.protect(true, true, a.manualSettle))
	mux.HandleFunc("GET /api/admin/runtime-logs", a.protect(true, false, a.runtimeLogs))
	mux.HandleFunc("GET /api/admin/audit-logs", a.protect(true, false, a.auditLogs))
	mux.HandleFunc("GET /api/admin/charges", a.protect(true, false, a.pendingCharges))
	mux.HandleFunc("POST /api/admin/charges/{id}/settle", a.protect(true, true, a.manualSettle))
	mux.HandleFunc("GET /api/admin/settings", a.protect(true, false, a.settings))
	mux.HandleFunc("PUT /api/admin/settings", a.protect(true, true, a.setSettings))
	mux.HandleFunc("GET /api/admin/settings/backup", a.protect(true, false, a.backupConfig))
	mux.HandleFunc("PUT /api/admin/settings/backup", a.protect(true, true, a.saveBackupConfig))
	mux.HandleFunc("POST /api/admin/settings/backup/test", a.protect(true, true, a.testBackupConfig))
	mux.HandleFunc("GET /api/admin/backups", a.protect(true, false, a.listBackups))
	mux.HandleFunc("POST /api/admin/backups", a.protect(true, true, a.createBackup))
	mux.HandleFunc("GET /api/admin/backups/{id}/download", a.protect(true, false, a.downloadBackup))
	mux.HandleFunc("DELETE /api/admin/backups/{id}", a.protect(true, true, a.deleteBackup))
	mux.HandleFunc("GET /api/admin/settings/email", a.protect(true, false, a.emailSettings))
	mux.HandleFunc("PUT /api/admin/settings/email", a.protect(true, true, a.saveEmailSettings))
	mux.HandleFunc("DELETE /api/admin/settings/email", a.protect(true, true, a.resetEmailSettings))
	mux.HandleFunc("POST /api/admin/settings/email/test", a.protect(true, true, a.testEmailSettings))
	mux.HandleFunc("POST /api/admin/oauth/{provider}/start", a.protect(true, true, a.startOAuth))
	mux.HandleFunc("POST /api/admin/oauth/{provider}/complete", a.protect(true, true, a.completeOAuth))
	mux.HandleFunc("GET /api/admin/accounts/{id}/config", a.protect(true, false, a.accountConfig))
	mux.HandleFunc("PUT /api/admin/accounts/{id}/config", a.protect(true, true, a.setAccountConfig))
	mux.Handle("/", serveUI())
	if a.gateway != nil {
		mux.HandleFunc("GET /v1/responses", a.gateway.ServeWebSocket)
		mux.HandleFunc("GET /responses", a.gateway.ServeWebSocket)
		for _, route := range []string{"POST /v1/messages", "POST /v1/messages/count_tokens", "POST /v1/responses", "POST /v1/responses/compact", "POST /responses", "POST /responses/compact"} {
			mux.HandleFunc(route, a.gateway.Serve)
		}
		mux.HandleFunc("POST /v1/chat/completions", a.gateway.ChatCompletions)
		mux.HandleFunc("POST /chat/completions", a.gateway.ChatCompletions)
		mux.HandleFunc("GET /v1/models", a.gateway.Models)
		mux.HandleFunc("GET /models", a.gateway.Models)
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/") {
			w.Header().Set("Content-Language", i18n.Language(r.Header.Get("Accept-Language")))
			w.Header().Add("Vary", "Accept-Language")
		}
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Content-Security-Policy", "default-src 'none'; frame-ancestors 'none'")
		if r.Method != "GET" && r.Method != "HEAD" && !a.sameOrigin(r) {
			writeError(w, 403, "请求来源不匹配")
			return
		}
		mux.ServeHTTP(w, r)
	})
}
func (a *API) sameOrigin(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}
	u, err := url.Parse(origin)
	scheme := "http"
	if r.TLS != nil || a.cfg.SecureCookies {
		scheme = "https"
	}
	return err == nil && u.User == nil && u.Scheme == scheme && u.Host == r.Host && u.Path == "" && u.RawQuery == "" && u.Fragment == ""
}
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}
func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": i18n.Translate(w.Header().Get("Content-Language"), message)})
}
func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	if strings.Split(r.Header.Get("Content-Type"), ";")[0] != "application/json" {
		writeError(w, 415, "需要 application/json")
		return false
	}
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	d := json.NewDecoder(r.Body)
	d.DisallowUnknownFields()
	if d.Decode(v) != nil || d.Decode(new(any)) != io.EOF {
		writeError(w, 400, "请求 JSON 无效或包含未知字段")
		return false
	}
	return true
}
func validCredentials(username, password string) bool {
	return usernamePattern.MatchString(username) && validPassword(password, "user")
}
func validPassword(password, _ string) bool {
	return len(password) >= 8 && len(password) <= 72
}
func randomToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}
func (a *API) allowLogin(r *http.Request) bool { return a.allowAttempt(r, "auth", 10) }
func (a *API) allowAttempt(r *http.Request, namespace string, limit int) bool {
	ip, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		ip = r.RemoteAddr
	}
	ip = namespace + ":" + ip
	a.mu.Lock()
	defer a.mu.Unlock()
	now := time.Now()
	if len(a.attempts) >= 1024 {
		for k, v := range a.attempts {
			if !now.Before(v.until) {
				delete(a.attempts, k)
			}
		}
	}
	v, ok := a.attempts[ip]
	if !ok || !now.Before(v.until) {
		if !ok && len(a.attempts) >= 1024 {
			return false
		}
		v = attempt{until: now.Add(time.Minute)}
	}
	if v.count >= limit {
		return false
	}
	v.count++
	a.attempts[ip] = v
	return true
}
func (a *API) setupStatus(w http.ResponseWriter, r *http.Request) {
	done, err := a.store.Initialized(r.Context())
	if err != nil {
		writeError(w, 500, "读取初始化状态失败")
		return
	}
	writeJSON(w, 200, map[string]bool{"initialized": done})
}
func (a *API) setup(w http.ResponseWriter, r *http.Request) {
	done, err := a.store.Initialized(r.Context())
	if err != nil {
		writeError(w, 500, "读取初始化状态失败")
		return
	}
	if done {
		writeError(w, 409, "管理员已初始化")
		return
	}
	if !a.allowLogin(r) {
		writeError(w, 429, "请求过于频繁")
		return
	}
	var in struct {
		Username   string `json:"username"`
		Password   string `json:"password"`
		SetupToken string `json:"setup_token"`
	}
	if !decode(w, r, &in) {
		return
	}
	expected := sha256.Sum256([]byte(a.cfg.SetupToken))
	provided := sha256.Sum256([]byte(in.SetupToken))
	if len(a.cfg.SetupToken) < 8 || subtle.ConstantTimeCompare(expected[:], provided[:]) != 1 {
		writeError(w, 403, "初始化口令无效")
		return
	}
	if !usernamePattern.MatchString(in.Username) || !validPassword(in.Password, "admin") {
		writeError(w, 400, "用户名需为 3–64 位字母数字或 _.-，管理员密码需为 8–72 字节")
		return
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(in.Password), 12)
	if err != nil {
		writeError(w, 500, "密码处理失败")
		return
	}
	id, err := a.store.CreateAdmin(r.Context(), in.Username, string(hash))
	if errors.Is(err, store.ErrInitialized) {
		writeError(w, 409, "管理员已初始化")
		return
	}
	if err != nil {
		writeError(w, 500, "创建管理员失败")
		return
	}
	writeJSON(w, 201, map[string]int64{"id": id})
}
func (a *API) login(w http.ResponseWriter, r *http.Request) {
	if !a.allowLogin(r) {
		writeError(w, 429, "登录过于频繁，请稍后重试")
		return
	}
	var in struct{ Username, Password string }
	if !decode(w, r, &in) {
		return
	}
	if len(in.Username) > 254 || len(in.Password) > 72 {
		writeError(w, 401, "账号或密码错误")
		return
	}
	if strings.Contains(in.Username, "@") {
		in.Username = strings.ToLower(strings.TrimSpace(in.Username))
	}
	user, err := a.store.UserByName(r.Context(), in.Username)
	hash := a.dummyHash
	if err == nil && user.PasswordSet {
		hash = []byte(user.PasswordHash)
	}
	match := bcrypt.CompareHashAndPassword(hash, []byte(in.Password)) == nil
	if err != nil || !user.Enabled || !user.PasswordSet || !match {
		writeError(w, 401, "账号或密码错误")
		return
	}
	token, err := randomToken()
	if err != nil {
		writeError(w, 500, "无法创建会话")
		return
	}
	csrf, err := randomToken()
	if err != nil {
		writeError(w, 500, "无法创建会话")
		return
	}
	digest := sha256.Sum256([]byte(token))
	expiry := time.Now().Add(12 * time.Hour)
	if err = a.store.SaveSession(r.Context(), digest[:], user.ID, csrf, expiry); err != nil {
		writeError(w, 500, "无法保存会话")
		return
	}
	http.SetCookie(w, &http.Cookie{Name: cookieName, Value: token, Path: "/", HttpOnly: true, Secure: a.cfg.SecureCookies, SameSite: http.SameSiteStrictMode, MaxAge: 12 * 3600, Expires: expiry})
	writeJSON(w, 200, map[string]any{"user": user, "csrf_token": csrf})
}
func (a *API) protect(admin, mutate bool, next func(http.ResponseWriter, *http.Request, identity)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie(cookieName)
		if err != nil || len(cookie.Value) != 43 {
			writeError(w, 401, "请先登录")
			return
		}
		digest := sha256.Sum256([]byte(cookie.Value))
		u, csrf, err := a.store.Session(r.Context(), digest[:])
		if err != nil {
			writeError(w, 401, "会话无效或已过期")
			return
		}
		if admin && u.Role != "admin" {
			writeError(w, 403, "需要管理员权限")
			return
		}
		if mutate && subtle.ConstantTimeCompare([]byte(csrf), []byte(r.Header.Get("X-CSRF-Token"))) != 1 {
			writeError(w, 403, "CSRF 校验失败")
			return
		}
		next(w, r, identity{user: u, csrf: csrf, hash: digest[:]})
	}
}
func (a *API) me(w http.ResponseWriter, r *http.Request, id identity) {
	writeJSON(w, 200, map[string]any{"user": id.user, "csrf_token": id.csrf})
}
func (a *API) logout(w http.ResponseWriter, r *http.Request, id identity) {
	if a.store.DeleteSession(r.Context(), id.hash) != nil {
		writeError(w, 500, "撤销会话失败")
		return
	}
	http.SetCookie(w, &http.Cookie{Name: cookieName, Path: "/", HttpOnly: true, Secure: a.cfg.SecureCookies, SameSite: http.SameSiteStrictMode, MaxAge: -1})
	writeJSON(w, 200, map[string]bool{"ok": true})
}
func (a *API) users(w http.ResponseWriter, r *http.Request, id identity) {
	users, err := a.store.Users(r.Context())
	if err != nil {
		writeError(w, 500, "读取用户失败")
		return
	}
	writeJSON(w, 200, users)
}
func (a *API) createUser(w http.ResponseWriter, r *http.Request, id identity) {
	var in struct {
		Username, Password string
		Limit5h            int64 `json:"limit_5h_microusd"`
		Limit7d            int64 `json:"limit_7d_microusd"`
	}
	if !decode(w, r, &in) {
		return
	}
	if !validCredentials(in.Username, in.Password) || in.Limit5h < 0 || in.Limit7d < 0 || in.Limit5h > 1000000000000000 || in.Limit7d > 1000000000000000 {
		writeError(w, 400, "账号、密码或额度格式无效")
		return
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(in.Password), 12)
	if err != nil {
		writeError(w, 500, "密码处理失败")
		return
	}
	user, err := a.store.CreateUser(r.Context(), id.user.ID, in.Username, string(hash), in.Limit5h, in.Limit7d)
	if err != nil {
		writeError(w, 409, "无法创建用户，请检查用户名是否重复")
		return
	}
	writeJSON(w, 201, map[string]int64{"id": user})
}
func (a *API) enableUser(w http.ResponseWriter, r *http.Request, id identity) {
	target, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || target <= 0 {
		writeError(w, 400, "用户 ID 无效")
		return
	}
	var in struct {
		Enabled *bool `json:"enabled"`
	}
	if !decode(w, r, &in) {
		return
	}
	if in.Enabled == nil {
		writeError(w, 400, "缺少 enabled")
		return
	}
	err = a.store.EnableUser(r.Context(), id.user.ID, target, *in.Enabled)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, 404, "普通用户不存在")
		return
	}
	if err != nil {
		writeError(w, 500, "更新用户失败")
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}

// Shutdown drains native sockets before their shared database is closed.
func (a *API) Shutdown(ctx context.Context) error {
	if err := a.backups.Close(ctx); err != nil {
		return err
	}
	if a.gateway != nil {
		return a.gateway.ShutdownWebSockets(ctx)
	}
	return nil
}
