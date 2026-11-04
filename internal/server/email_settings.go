package server

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"fiftyapi/internal/credentials"
	"fiftyapi/internal/mailer"
)

const emailSettingName = "email_settings_v1"
const emailSettingBinding = "fiftyapi:email-settings:v1"

type emailSettings struct {
	Enabled   bool   `json:"enabled"`
	PublicURL string `json:"public_url"`
	Host      string `json:"host"`
	Port      string `json:"port"`
	From      string `json:"from"`
	Username  string `json:"username"`
	Password  string `json:"password,omitempty"`
	TLSMode   string `json:"tls_mode"`
}

func (c emailSettings) smtp() *mailer.SMTP {
	return &mailer.SMTP{Host: c.Host, Port: c.Port, From: c.From, Username: c.Username, Password: c.Password, TLSMode: c.TLSMode}
}
func (c emailSettings) validate() error {
	if len(c.PublicURL) > 2048 || len(c.Host) > 255 || len(c.From) > 320 || len(c.Username) > 320 || len(c.Password) > 4096 {
		return errors.New("邮件配置字段过长")
	}
	if c.PublicURL != "" && !validPublicURL(c.PublicURL) {
		return errors.New("公共访问地址应为 HTTP 或 HTTPS 源站地址，不含路径、参数或片段")
	}
	if c.Enabled {
		if !validPublicURL(c.PublicURL) {
			return errors.New("请填写邀请链接使用的公共访问地址")
		}
		return c.smtp().Validate()
	}
	return nil
}

func (a *API) readEmailSettings(ctx context.Context) (emailSettings, string, error) {
	encoded, exists, err := a.store.Setting(ctx, emailSettingName)
	if err != nil {
		return emailSettings{}, "", err
	}
	if !exists {
		c := emailSettings{Enabled: a.cfg.Mailer != nil, PublicURL: a.cfg.PublicURL, Port: "587", TLSMode: "starttls"}
		if smtp, ok := a.cfg.Mailer.(*mailer.SMTP); ok {
			c.Host = smtp.Host
			c.Port = smtp.Port
			c.From = smtp.From
			c.Username = smtp.Username
			c.Password = smtp.Password
			c.TLSMode = smtp.TLSMode
		}
		return c, "environment", nil
	}
	if len(a.cfg.SettingsKey) != 32 {
		return emailSettings{}, "", errors.New("邮件配置加密密钥不可用")
	}
	data, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return emailSettings{}, "", err
	}
	plain, err := credentials.Decrypt(a.cfg.SettingsKey, emailSettingBinding, data)
	if err != nil {
		return emailSettings{}, "", err
	}
	var c emailSettings
	if err = json.Unmarshal(plain, &c); err != nil {
		return c, "", err
	}
	if err = c.validate(); err != nil {
		return c, "", err
	}
	return c, "database", nil
}
func (a *API) invitationSender(ctx context.Context) (mailer.Sender, string, error) {
	c, source, err := a.readEmailSettings(ctx)
	if err != nil {
		return nil, "", err
	}
	if !c.Enabled {
		return nil, c.PublicURL, nil
	}
	if source == "environment" {
		return a.cfg.Mailer, c.PublicURL, nil
	}
	return c.smtp(), c.PublicURL, nil
}
func (a *API) emailSettings(w http.ResponseWriter, r *http.Request, id identity) {
	c, source, err := a.readEmailSettings(r.Context())
	if err != nil {
		writeError(w, 500, "无法读取邮件配置，请检查原主密钥或恢复配置")
		return
	}
	hasPassword := c.Password != ""
	c.Password = ""
	writeJSON(w, 200, map[string]any{"config": c, "password_set": hasPassword, "source": source, "editable": len(a.cfg.SettingsKey) == 32})
}
func (a *API) saveEmailSettings(w http.ResponseWriter, r *http.Request, id identity) {
	if len(a.cfg.SettingsKey) != 32 {
		writeError(w, 503, "邮件配置加密密钥不可用")
		return
	}
	var in struct {
		emailSettings
		ClearPassword bool `json:"clear_password"`
	}
	if !decode(w, r, &in) {
		return
	}
	if in.ClearPassword && in.Password != "" {
		writeError(w, 400, "不能同时清除和设置密码")
		return
	}
	a.emailMu.Lock()
	defer a.emailMu.Unlock()
	old, _, err := a.readEmailSettings(r.Context())
	if err != nil {
		writeError(w, 500, "无法读取原邮件配置；可先恢复环境变量配置")
		return
	}
	if in.Password == "" && !in.ClearPassword {
		in.Password = old.Password
	}
	in.Host = strings.TrimSpace(in.Host)
	in.Port = strings.TrimSpace(in.Port)
	in.From = strings.TrimSpace(in.From)
	in.Username = strings.TrimSpace(in.Username)
	in.PublicURL = strings.TrimRight(strings.TrimSpace(in.PublicURL), "/")
	if in.Port == "" {
		in.Port = "587"
	}
	if in.TLSMode == "" {
		in.TLSMode = "starttls"
	}
	if err = in.emailSettings.validate(); err != nil {
		writeError(w, 400, err.Error())
		return
	}
	plain, err := json.Marshal(in.emailSettings)
	if err != nil {
		writeError(w, 500, "无法保存邮件配置")
		return
	}
	sealed, err := credentials.Encrypt(a.cfg.SettingsKey, emailSettingBinding, plain)
	if err != nil {
		writeError(w, 500, "无法加密邮件配置")
		return
	}
	encoded := base64.StdEncoding.EncodeToString(sealed)
	if err = a.store.SaveSetting(r.Context(), id.user.ID, emailSettingName, &encoded); err != nil {
		writeError(w, 500, "保存邮件配置失败")
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}
func (a *API) resetEmailSettings(w http.ResponseWriter, r *http.Request, id identity) {
	a.emailMu.Lock()
	defer a.emailMu.Unlock()
	if err := a.store.SaveSetting(r.Context(), id.user.ID, emailSettingName, nil); err != nil {
		writeError(w, 500, "恢复邮件配置失败")
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}
func (a *API) testEmailSettings(w http.ResponseWriter, r *http.Request, id identity) {
	if !a.allowAttempt(r, "email-test", 3) {
		writeError(w, 429, "测试邮件过于频繁，请稍后重试")
		return
	}
	var in struct {
		Email string `json:"email"`
	}
	if !decode(w, r, &in) {
		return
	}
	in.Email = strings.TrimSpace(in.Email)
	if len(in.Email) > 254 {
		writeError(w, 400, "收件邮箱无效")
		return
	}
	sender, _, err := a.invitationSender(r.Context())
	if err != nil {
		writeError(w, 500, "无法读取邮件配置")
		return
	}
	smtp, ok := sender.(*mailer.SMTP)
	if !ok || smtp == nil {
		writeError(w, 400, "请先保存并启用 SMTP 配置")
		return
	}
	if err = smtp.SendTest(r.Context(), in.Email); err != nil {
		writeError(w, 400, err.Error())
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}
