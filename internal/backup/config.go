package backup

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fiftyapi/internal/credentials"
	"net/url"
	"regexp"
	"strings"
)

const settingName = "backup_settings_v1"
const binding = "fiftyapi:backup-settings:v1"

type Config struct {
	Endpoint      string `json:"endpoint"`
	Region        string `json:"region"`
	Bucket        string `json:"bucket"`
	Prefix        string `json:"prefix"`
	AccessKey     string `json:"access_key,omitempty"`
	SecretKey     string `json:"secret_key,omitempty"`
	PathStyle     bool   `json:"path_style"`
	AutoEnabled   bool   `json:"auto_enabled"`
	DailyTime     string `json:"daily_time"`
	RetentionDays int    `json:"retention_days"`
}

func DefaultConfig() Config {
	return Config{Region: "auto", Prefix: "backups/", DailyTime: "03:00", RetentionDays: 14, PathStyle: true}
}
func (c Config) Validate() error {
	if len(c.Endpoint) > 2048 {
		return errors.New("端点地址过长")
	}
	u, err := url.Parse(c.Endpoint)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.RawPath != "" || strings.Trim(u.Path, "/") != "" {
		return errors.New("端点地址需为 HTTPS 源站地址，不含路径、查询参数或凭据")
	}
	if !regexp.MustCompile(`^[a-z0-9][a-z0-9.-]{1,61}[a-z0-9]$`).MatchString(c.Bucket) || strings.Contains(c.Bucket, "..") {
		return errors.New("存储桶名称无效")
	}
	if !regexp.MustCompile(`^[a-zA-Z0-9-]{1,64}$`).MatchString(c.Region) {
		return errors.New("区域格式无效")
	}
	if len(c.Prefix) > 200 || strings.ContainsAny(c.Prefix, "\r\n\x00\\") || strings.HasPrefix(c.Prefix, "/") {
		return errors.New("Key 前缀无效")
	}
	for _, part := range strings.Split(c.Prefix, "/") {
		if part == "." || part == ".." {
			return errors.New("Key 前缀不能包含相对路径")
		}
	}
	if c.AccessKey == "" || c.SecretKey == "" || len(c.AccessKey) > 256 || len(c.SecretKey) > 4096 || strings.ContainsAny(c.AccessKey+c.SecretKey, "\r\n\x00") {
		return errors.New("请填写有效的 Access Key ID 和 Secret Access Key")
	}
	if c.RetentionDays < 1 || c.RetentionDays > 3650 {
		return errors.New("保留天数需为 1–3650")
	}
	if !regexp.MustCompile(`^(?:[01][0-9]|2[0-3]):[0-5][0-9]$`).MatchString(c.DailyTime) {
		return errors.New("自动备份时间需为 HH:mm")
	}
	return nil
}
func (s *Service) Config(ctx context.Context) (Config, error) {
	value, exists, err := s.store.Setting(ctx, settingName)
	if err != nil {
		return Config{}, err
	}
	if !exists {
		return DefaultConfig(), nil
	}
	raw, err := base64.StdEncoding.DecodeString(value)
	if err != nil {
		return Config{}, errors.New("备份配置无法读取")
	}
	plain, err := credentials.Decrypt(s.key, binding, raw)
	if err != nil {
		return Config{}, errors.New("备份配置无法解密，请检查原主密钥")
	}
	defer clear(plain)
	var c Config
	if json.Unmarshal(plain, &c) != nil {
		return c, errors.New("备份配置格式错误")
	}
	return c, nil
}
func (s *Service) ResolveConfig(ctx context.Context, c Config) (Config, error) {
	old, err := s.Config(ctx)
	if err != nil {
		return c, err
	}
	if c.AccessKey == "" {
		c.AccessKey = old.AccessKey
	}
	if c.SecretKey == "" {
		c.SecretKey = old.SecretKey
	}
	c.Endpoint = strings.TrimRight(strings.TrimSpace(c.Endpoint), "/")
	c.Region = strings.TrimSpace(c.Region)
	c.Bucket = strings.TrimSpace(c.Bucket)
	c.Prefix = strings.TrimSpace(c.Prefix)
	if c.Prefix != "" && !strings.HasSuffix(c.Prefix, "/") {
		c.Prefix += "/"
	}
	return c, c.Validate()
}
func (s *Service) SaveConfig(ctx context.Context, actor int64, c Config) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, err := s.ResolveConfig(ctx, c)
	if err != nil {
		return err
	}
	raw, _ := json.Marshal(c)
	defer clear(raw)
	encrypted, err := credentials.Encrypt(s.key, binding, raw)
	if err != nil {
		return errors.New("备份配置加密失败")
	}
	value := base64.StdEncoding.EncodeToString(encrypted)
	return s.store.SaveSetting(ctx, actor, settingName, &value)
}
