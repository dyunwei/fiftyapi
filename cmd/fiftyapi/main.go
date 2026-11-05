package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"syscall"
	"time"

	"fiftyapi/internal/accounts"
	"fiftyapi/internal/backup"
	"fiftyapi/internal/buildinfo"
	"fiftyapi/internal/configenv"
	"fiftyapi/internal/credentials"
	"fiftyapi/internal/mailer"
	"fiftyapi/internal/providers"
	"fiftyapi/internal/server"
	"fiftyapi/internal/store"
)

func main() {
	if err := run(); err != nil {
		log.Print(err)
		os.Exit(1)
	}
}
func env(name, fallback string) string {
	if value := configenv.Value(name); value != "" {
		return value
	}
	return fallback
}

func databasePath() (string, error) {
	if path := configenv.Value("FIFTYAPI_DB_PATH"); path != "" {
		return path, nil
	}
	return filepath.Join(env("FIFTYAPI_DATA_DIR", "data"), "fiftyapi.db"), nil
}
func run() error {
	command := "serve"
	args := os.Args[1:]
	if len(args) > 0 {
		command = args[0]
		args = args[1:]
	}
	if command == "version" && len(args) == 0 {
		fmt.Printf("FiftyAPI %s (%s)\n", buildinfo.Version(), buildinfo.Commit)
		return nil
	}
	if command == "restore" {
		return restoreBackup(args)
	}
	if command != "serve" && command != "import-antigravity" {
		return errors.New("用法：fiftyapi [serve | import-antigravity | restore | version]")
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	dbPath, err := databasePath()
	if err != nil {
		return err
	}
	if command == "serve" {
		unlock, err := store.LockServerPath(dbPath)
		if err != nil {
			return err
		}
		defer unlock()
	}
	key, err := masterKey(dbPath, command == "serve")
	if err != nil {
		return err
	}
	s, err := store.Open(ctx, dbPath)
	if err != nil {
		return fmt.Errorf("打开数据库失败：%w", err)
	}
	defer s.Close()
	accountService, err := accounts.New(s, key)
	if err != nil {
		return err
	}
	defer func() {
		drain, stop := context.WithTimeout(context.Background(), 35*time.Second)
		defer stop()
		_ = accountService.Shutdown(drain)
	}()
	if command == "import-antigravity" {
		return importAccount(ctx, args, s, accountService)
	}
	if len(args) > 0 {
		return errors.New("serve 使用环境变量配置，不接受额外参数")
	}
	if err = s.RecoverInFlight(ctx); err != nil {
		return err
	}
	maintenanceDone := make(chan struct{})
	go func() {
		defer close(maintenanceDone)
		ticker := time.NewTicker(time.Minute)
		defer ticker.Stop()
		count := 0
		for {
			accountService.Maintain(ctx)
			if count%5 == 0 {
				cleanup, cancel := context.WithTimeout(ctx, 10*time.Second)
				if e := s.CleanupLogs(cleanup); e != nil && ctx.Err() == nil {
					log.Print("请求日志清理失败")
					s.RecordRuntime(cleanup, store.RuntimeLog{Level: "error", Module: "database", Event: "log_cleanup", Message: "请求日志清理失败"})
				}
				if e := s.CleanupRuntimeLogs(cleanup); e != nil && ctx.Err() == nil {
					s.RecordRuntime(cleanup, store.RuntimeLog{Level: "error", Module: "database", Event: "runtime_cleanup", Message: "运行日志清理失败"})
				}
				cancel()
			}
			count++
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
	defer func() { stop(); <-maintenanceDone }()

	setupToken := ""
	done, err := s.Initialized(ctx)
	if err != nil {
		return err
	}
	if !done {
		setupToken, err = initializationToken(dbPath)
		if err != nil {
			return err
		}
		if configenv.Value("FIFTYAPI_SETUP_TOKEN") == "" {
			log.Printf("Setup token file / 初始化口令文件: %s (read locally; keep private)", filepath.Join(filepath.Dir(dbPath), "setup-token"))
		}
	}
	secure, err := strconv.ParseBool(env("FIFTYAPI_COOKIE_SECURE", "false"))
	if err != nil {
		return errors.New("FIFTYAPI_COOKIE_SECURE 必须为 true 或 false")
	}
	smtpSender, err := mailer.FromEnv()
	if err != nil {
		return err
	}
	var sender mailer.Sender
	if smtpSender != nil {
		sender = smtpSender
	}
	api, err := server.New(s, server.Config{SettingsKey: key, SetupToken: setupToken, SecureCookies: secure, Mailer: sender, PublicURL: configenv.Value("FIFTYAPI_PUBLIC_URL")})
	if err != nil {
		return err
	}
	api.AttachAccounts(accountService)
	if err = api.StartBackups(ctx); err != nil {
		return fmt.Errorf("启动备份任务失败：%w", err)
	}
	probeCtx, stopProbes := context.WithCancel(ctx)
	probeDone := make(chan struct{})
	go func() { defer close(probeDone); api.RunMorningProbes(probeCtx) }()
	defer func() { stopProbes(); <-probeDone }()
	defer func() {
		drain, stop := context.WithTimeout(context.Background(), 5*time.Second)
		defer stop()
		_ = api.Shutdown(drain)
	}()
	httpServer := &http.Server{Addr: env("FIFTYAPI_LISTEN", "127.0.0.1:8080"), Handler: api.Handler(), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 16 << 10}
	doneCh := make(chan error, 1)
	go func() {
		log.Printf("FiftyAPI API listening on %s", httpServer.Addr)
		listener, listenErr := net.Listen("tcp", httpServer.Addr)
		if listenErr != nil {
			s.RecordRuntime(ctx, store.RuntimeLog{Level: "error", Module: "server", Event: "listen_failed", Message: "服务启动失败：无法监听端口"})
			doneCh <- listenErr
			return
		}
		s.RecordRuntime(ctx, store.RuntimeLog{Level: "info", Module: "server", Event: "started", Message: "HTTP 服务已启动"})
		doneCh <- httpServer.Serve(listener)
	}()
	select {
	case err := <-doneCh:
		if !errors.Is(err, http.ErrServerClosed) {
			s.RecordRuntime(ctx, store.RuntimeLog{Level: "error", Module: "server", Event: "serve_failed", Message: "HTTP 服务异常退出"})
			return err
		}
		return nil
	case <-ctx.Done():
		s.RecordRuntime(ctx, store.RuntimeLog{Level: "info", Module: "server", Event: "stopping", Message: "服务正在关闭"})
		shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err = api.Shutdown(shutdown); err != nil {
			s.RecordRuntime(ctx, store.RuntimeLog{Level: "error", Module: "server", Event: "shutdown_failed", Message: "关闭服务时无法及时释放原生连接"})
			httpServer.Close()
			return err
		}
		if err = httpServer.Shutdown(shutdown); err != nil {
			s.RecordRuntime(ctx, store.RuntimeLog{Level: "error", Module: "server", Event: "shutdown_failed", Message: "HTTP 服务关闭超时，已强制关闭"})
			httpServer.Close()
			return err
		}
		s.RecordRuntime(ctx, store.RuntimeLog{Level: "info", Module: "server", Event: "stopped", Message: "HTTP 服务已关闭"})
		return nil
	}
}

func readJSON(path string, dst any) error {
	file, err := os.Open(path)
	if err != nil {
		return errors.New("无法打开配置文件")
	}
	defer file.Close()
	decoder := json.NewDecoder(io.LimitReader(file, 1<<20))
	decoder.DisallowUnknownFields()
	if decoder.Decode(dst) != nil || decoder.Decode(new(any)) != io.EOF {
		return errors.New("配置 JSON 无效")
	}
	return nil
}
func importAccount(ctx context.Context, args []string, s *store.Store, service *accounts.Service) error {
	flags := flag.NewFlagSet("import-antigravity", flag.ContinueOnError)
	name := flags.String("name", "", "账号名称")
	admin := flags.String("admin", "", "已初始化的管理员用户名")
	configPath := flags.String("config", "examples/probe/antigravity.json", "供应商配置 JSON")
	session := flags.String("session-file", ".local/antigravity.session", "探针加密会话")
	keyPath := flags.String("key-file", ".local/providers.key", "已有探针密钥文件")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *name == "" || *admin == "" || flags.NArg() != 0 {
		return errors.New("导入需要 --name 和 --admin，且不接受额外参数")
	}
	actor, err := s.UserByName(ctx, *admin)
	if err != nil || actor.Role != "admin" || !actor.Enabled {
		return errors.New("请先初始化指定的管理员")
	}
	// Unlike the developer helper, import never creates a missing probe key.
	if _, err = os.Stat(*keyPath); err != nil {
		return errors.New("找不到原探针密钥，未生成新密钥")
	}
	probeKey, err := credentials.LoadKey(*keyPath)
	if err != nil {
		return err
	}
	var cfg providers.Config
	if err = readJSON(*configPath, &cfg); err != nil {
		return err
	}
	if cfg.AntigravityVersion == "" {
		cfg.AntigravityVersion = os.Getenv("ANTIGRAVITY_USER_AGENT_VERSION")
	}
	if cfg.AntigravityIdentity == "" {
		cfg.AntigravityIdentity = os.Getenv("ANTIGRAVITY_IDENTITY_PROMPT")
	}
	binding, _ := json.Marshal([]string{cfg.Provider, cfg.ClientID, cfg.AuthorizeURL, cfg.TokenURL, cfg.RedirectURI, cfg.Scope, cfg.ModelsURL})
	data, err := credentials.Load(*session, probeKey, string(binding))
	if err != nil {
		return err
	}
	var saved struct {
		Token     providers.Token `json:"token"`
		ExpiresAt time.Time       `json:"expires_at"`
	}
	if json.Unmarshal(data, &saved) != nil {
		return errors.New("探针会话格式错误")
	}
	id, err := service.Import(ctx, actor.ID, *name, accounts.Credential{Config: cfg, Token: saved.Token, ExpiresAt: saved.ExpiresAt})
	if err != nil {
		return errors.New("导入失败，请检查配置、密钥和账号名称是否重复")
	}
	fmt.Printf("账号已导入 SQLite，ID=%d；凭据已使用服务主密钥重新加密，状态 authorized。\n", id)
	return nil
}

func restoreBackup(args []string) error {
	flags := flag.NewFlagSet("restore", flag.ContinueOnError)
	source := flags.String("from", "", "备份 tar.gz 文件")
	output := flags.String("output", "", "恢复到不存在的新目录")
	keyFile := flags.String("key-file", "", "原 master.key 文件（可选，否则读取现有环境或数据目录）")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *source == "" || *output == "" || flags.NArg() != 0 {
		return errors.New("用法：fiftyapi restore --from backup.tar.gz --output 新目录 [--key-file 原master.key]")
	}
	path, err := databasePath()
	if err != nil {
		return err
	}
	unlock, err := store.LockServerPath(path)
	if err != nil {
		return err
	}
	defer unlock()
	var key []byte
	if *keyFile != "" {
		var value string
		value, err = readSecret(*keyFile)
		if err == nil {
			key, err = base64.StdEncoding.DecodeString(value)
		}
	} else {
		key, err = masterKey(path, false)
	}
	if err != nil {
		return err
	}
	defer clear(key)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err = backup.Restore(ctx, *source, *output, key); err != nil {
		return err
	}
	fmt.Printf("恢复完成：%s。原数据库未覆盖；原主密钥已保存到恢复目录。启动前切换 FIFTYAPI_DB_PATH，并检查备份计划和上游授权。\n", *output)
	return nil
}
