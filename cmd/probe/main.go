package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"time"

	"fiftyapi/internal/providers"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "探针失败：", err)
		os.Exit(1)
	}
}
func run() error {
	configPath := flag.String("config", "", "不含令牌的供应商配置 JSON 路径")
	refresh := flag.Bool("refresh", false, "换码后执行一次刷新（可能轮换 refresh_token）")
	onboard := flag.Bool("onboard", false, "缺少 Antigravity 项目时按上游默认 tier 执行账号初始化")
	sessionPath := flag.String("session-file", "", "可选：本地加密会话文件")
	keyPath := flag.String("key-file", "", "可选：会话加密密钥文件（首次创建为 0600）")
	model := flag.String("model", "", "可选：执行最小文本与 SSE 推理验证的模型名（消耗上游额度）")
	tools := flag.Bool("tools", false, "执行两轮无副作用工具调用验证，需同时指定 -model")
	noLogin := flag.Bool("no-login", false, "仅复用已保存会话，缺少会话时不发起浏览器授权")
	flag.Parse()
	if *configPath == "" {
		flag.Usage()
		return errors.New("请指定 -config")
	}
	f, err := os.Open(*configPath)
	if err != nil {
		return errors.New("无法打开配置文件")
	}
	defer f.Close()
	var c providers.Config
	dec := json.NewDecoder(io.LimitReader(f, 1<<20))
	dec.DisallowUnknownFields()
	if dec.Decode(&c) != nil {
		return errors.New("配置 JSON 无效或包含未知字段")
	}
	if err = dec.Decode(new(any)); err != io.EOF {
		return errors.New("配置包含多余内容")
	}
	c.AllowOnboard = *onboard
	if *tools && *model == "" {
		return errors.New("-tools 需要同时指定 -model")
	}
	if *model != "" && c.Provider != "antigravity" {
		return errors.New("推理探针暂仅支持 Antigravity")
	}
	if c.AntigravityIdentity == "" {
		c.AntigravityIdentity = os.Getenv("ANTIGRAVITY_IDENTITY_PROMPT")
	}
	if c.Provider == "antigravity" && c.AntigravityVersion == "" {
		c.AntigravityVersion = strings.TrimSpace(os.Getenv("ANTIGRAVITY_USER_AGENT_VERSION"))
	}
	if *onboard && c.Provider != "antigravity" {
		return errors.New("-onboard 仅适用于 Antigravity")
	}
	if *onboard {
		fmt.Println("已启用 Antigravity onboardUser：缺少项目时将按上游默认 tier 初始化账号。")
	}
	if err := c.Validate(); err != nil {
		return err
	}
	secret := ""
	if c.ClientSecretEnv != "" {
		secret = strings.TrimSpace(os.Getenv(c.ClientSecretEnv))
		if secret == "" {
			return errors.New("缺少配置指定的 client secret 环境变量")
		}
	}
	store, saved, err := openSession(*sessionPath, *keyPath, c)
	if err != nil {
		return err
	}
	if store != nil {
		fmt.Println("已启用本地加密会话；授权成功后会立即保存，项目发现失败也可继续使用。")
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	client := providers.NewClient()
	var t providers.Token
	var expires time.Time
	if saved != nil {
		t = saved.Token
		expires = saved.ExpiresAt
		fmt.Println("已读取本地加密会话，无需重新浏览器授权。")
	} else {
		if *noLogin {
			return errors.New("没有可复用的本地会话，未发起授权")
		}
		s, authURL, startErr := providers.Start(c)
		if startErr != nil {
			return startErr
		}
		fmt.Println("在本机浏览器打开以下授权链接：\n" + authURL)
		fmt.Println("粘贴完整回调 URL 或供应商显示的 code#state，然后回车。不要将其提交到仓库或聊天。")
		input := bufio.NewScanner(os.Stdin)
		input.Buffer(make([]byte, 4096), 65536)
		if !input.Scan() {
			return errors.New("未读取到授权结果")
		}
		t, err = providers.Exchange(ctx, client, c, s, strings.TrimSpace(input.Text()), secret)
		if err != nil {
			return fmt.Errorf("OAuth 换码阶段：%w", err)
		}
		expires = time.Now().Add(time.Duration(t.ExpiresIn) * time.Second)
		if err = store.save(t, expires); err != nil {
			return fmt.Errorf("授权成功，但保存加密会话失败：%w", err)
		}
		fmt.Printf("换码成功；有效期 %d 秒；返回刷新令牌：%t。\n", t.ExpiresIn, t.RefreshToken != "")
		if store == nil {
			fmt.Println("令牌仅保存在进程内存。")
		}
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	if *refresh || time.Until(expires) <= 3*time.Minute {
		t, err = providers.Refresh(ctx, client, c, t, secret)
		if err != nil {
			return fmt.Errorf("OAuth 刷新阶段：%w", err)
		}
		expires = time.Now().Add(time.Duration(t.ExpiresIn) * time.Second)
		if err = store.save(t, expires); err != nil {
			return fmt.Errorf("令牌已刷新，但保存失败：%w", err)
		}
		fmt.Println("刷新成功。")
	}
	models, err := providers.Models(ctx, client, c, t)
	if err != nil {
		return fmt.Errorf("模型发现阶段：%w", err)
	}
	fmt.Printf("模型目录读取成功，共 %d 项（尚不代表调用权限）：\n", len(models))
	if err = json.NewEncoder(os.Stdout).Encode(models); err != nil {
		return err
	}
	if *model != "" {
		fmt.Printf("开始推理验证，指定模型 %s，不自动换模型。\n", *model)
		results, inferErr := providers.CheckAntigravity(ctx, client, c, t, *model, *tools)
		if err = json.NewEncoder(os.Stdout).Encode(results); err != nil {
			return err
		}
		if inferErr != nil {
			return fmt.Errorf("推理验证阶段：%w", inferErr)
		}
		fmt.Println("指定模型的推理验证通过。")
	}
	return nil
}
