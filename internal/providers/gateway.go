package providers

import (
	"context"
	"encoding/json"
	"errors"
	"fiftyapi/internal/modelref"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"strings"
)

// RelayChainHeader carries the visited instances for relay loop detection.
const RelayChainHeader = "X-FiftyAPI-Relay-Chain"
const RelayHopLimit = 4

type GatewayModel = modelref.Mapping
type GatewayConfig struct {
	Platform  string         `json:"platform"`
	BaseURL   string         `json:"base_url"`
	Protocols []string       `json:"protocols"`
	Discovery string         `json:"discovery"`
	Models    []GatewayModel `json:"models"`
	AuthMode  string         `json:"auth_mode"`
	AllowHTTP bool           `json:"allow_http"`
	WebSocket bool           `json:"websocket"`
	Compact   bool           `json:"compact"`
}

func (g GatewayConfig) Supports(protocol string) bool {
	for _, p := range g.Protocols {
		if p == protocol {
			return true
		}
	}
	return false
}
func NormalizeGatewayBase(raw string, allowHTTP bool) (string, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Opaque != "" || u.RawPath != "" {
		return "", errors.New("网关 Base URL 无效，请勿包含凭据、查询参数或片段")
	}
	if u.Scheme != "https" && !(u.Scheme == "http" && allowHTTP) {
		return "", errors.New("网关默认要求 HTTPS；可信内网 HTTP 需明确启用")
	}
	rawPath := strings.TrimRight(u.Path, "/")
	clean := path.Clean(rawPath)
	if clean == "." {
		clean = ""
	}
	if clean != rawPath {
		return "", errors.New("Base URL 路径不能包含重复斜杠或相对路径")
	}
	if strings.HasSuffix(clean, "/messages") || strings.HasSuffix(clean, "/responses") || strings.HasSuffix(clean, "/models") || strings.HasSuffix(clean, "/chat/completions") {
		return "", errors.New("请填写网关 Base URL，不要填写具体接口地址")
	}
	if !strings.HasSuffix(clean, "/v1") {
		clean += "/v1"
	}
	u.Path = clean
	u.Host = strings.ToLower(u.Host)
	return strings.TrimRight(u.String(), "/"), nil
}
func (c Config) ValidateGateway() error {
	g := c.Gateway
	if strings.ContainsAny(c.UserAgent, "\r\n") {
		return errors.New("User-Agent 格式无效")
	}
	if g == nil {
		return errors.New("缺少 API Key 网关配置")
	}
	switch g.Platform {
	case "sub2api", "fiftyapi", "new-api", "custom":
	default:
		return errors.New("网关平台无效")
	}
	if _, err := NormalizeGatewayBase(g.BaseURL, g.AllowHTTP); err != nil {
		return err
	}
	if len(g.Protocols) == 0 || len(g.Protocols) > 3 {
		return errors.New("请选择 Chat Completions、Messages 或 Responses 协议")
	}
	seen := map[string]bool{}
	for _, p := range g.Protocols {
		if !modelref.ValidProtocol(p) || seen[p] {
			return errors.New("网关协议无效或重复")
		}
		seen[p] = true
	}
	if g.AuthMode != "bearer" && g.AuthMode != "x-api-key" {
		return errors.New("API Key 认证方式无效")
	}
	if g.Discovery != "auto" && g.Discovery != "manual" {
		return errors.New("模型发现方式应为 auto 或 manual")
	}
	if len(g.Models) > 1000 || (g.Discovery == "manual" && len(g.Models) == 0) {
		return errors.New("手工模型列表需为 1–1000 项")
	}

	aliases := map[string]bool{}
	targets := map[string]bool{}
	for _, m := range g.Models {
		if !ValidModelID(m.UpstreamID) || (m.ID != "" && !ValidModelID(m.ID)) || len(m.SupportedProtocols()) == 0 || (m.Protocol != "" && len(m.Protocols) > 0) {
			return errors.New("模型需填写有效 ID 和协议；新旧协议字段不能同时填写")
		}
		if m.ID != "" && aliases[m.ID] {
			return errors.New("模型别名重复；同一模型的多个协议请在同一行勾选")
		}
		if m.ID != "" {
			aliases[m.ID] = true
		}
		for _, p := range m.SupportedProtocols() {
			target := p + "\x00" + m.UpstreamID
			if !g.Supports(p) || !modelref.ValidProtocol(p) || targets[target] {
				return errors.New("模型协议未启用或映射目标重复")
			}
			targets[target] = true
		}
	}
	if c.GatewayURL != "" {
		return errors.New("API Key 网关通过 Base URL 配置，不使用 OAuth 推理地址")
	}
	if err := ValidateGatewayHeaders(c.GatewayHeaders); err != nil {
		return err
	}
	return ValidateGatewayHeaders(c.ModelsHeaders)
}
func ValidModelID(id string) bool {
	return len(id) > 0 && len(id) <= 255 && strings.TrimSpace(id) == id && !strings.ContainsAny(id, "\r\n\x00\t ")
}
func ValidateGatewayHeaders(headers map[string]string) error {
	token := regexp.MustCompile("^[!#$%&'*+.^_`|~0-9A-Za-z-]+$")
	for k, v := range headers {
		if !token.MatchString(k) || strings.ContainsAny(v, "\r\n") {
			return errors.New("请求头格式无效")
		}
		switch strings.ToLower(k) {
		case "authorization", "cookie", "x-api-key", "proxy-authorization", "host", "connection", "content-length", "transfer-encoding", "x-fiftyapi-relay-chain":
			return errors.New("请求头不能覆盖认证、连接或循环保护字段")
		}
	}
	return nil
}
func GatewayAuth(header http.Header, c Config, key string) {
	if c.Gateway.AuthMode == "x-api-key" {
		header.Set("x-api-key", key)
	} else {
		header.Set("Authorization", "Bearer "+key)
	}
}
func GatewayEndpoint(g GatewayConfig, endpoint string) (string, error) {
	base, err := NormalizeGatewayBase(g.BaseURL, g.AllowHTTP)
	if err != nil {
		return "", err
	}
	return base + "/" + endpoint, nil
}
func (c Config) GatewayModels(ctx context.Context, client *http.Client, key, chain string) ([]GatewayModel, error) {
	g := c.Gateway
	models := []GatewayModel{}
	if g.Discovery == "auto" {
		endpoint, err := GatewayEndpoint(*g, "models")
		if err != nil {
			return nil, err
		}
		ids := []string{}
		cursor := ""
		seenCursors := map[string]bool{}
		for pageIndex := 0; ; pageIndex++ {
			if pageIndex >= 10 {
				return nil, errors.New("模型目录分页超过 10 页，请使用手工配置")
			}
			u, _ := url.Parse(endpoint)
			if cursor != "" {
				q := u.Query()
				q.Set("after_id", cursor)
				u.RawQuery = q.Encode()
			}
			req, e := http.NewRequestWithContext(ctx, "GET", u.String(), nil)
			if e != nil {
				return nil, errors.New("无法构造网关目录请求")
			}
			for k, v := range c.ModelsHeaders {
				req.Header.Set(k, v)
			}
			GatewayAuth(req.Header, c, key)
			req.Header.Set("Accept", "application/json")
			req.Header.Set(RelayChainHeader, chain)
			b, e := perform(client, req)
			if e != nil {
				return nil, e
			}
			pageIDs, e := ParseModels(b)
			if e != nil {
				return nil, e
			}
			ids = append(ids, pageIDs...)
			if len(ids) > 1000 {
				return nil, errors.New("上游模型目录超过 1000 项，请使用手工选择")
			}
			var pagination struct {
				HasMore bool   `json:"has_more"`
				LastID  string `json:"last_id"`
				Data    []struct {
					ID string `json:"id"`
				} `json:"data"`
			}
			json.Unmarshal(b, &pagination)
			if !pagination.HasMore {
				break
			}
			next := pagination.LastID
			if next == "" && len(pagination.Data) > 0 {
				next = pagination.Data[len(pagination.Data)-1].ID
			}
			if !ValidModelID(next) || seenCursors[next] {
				return nil, errors.New("上游目录分页游标缺失或重复")
			}
			seenCursors[next] = true
			cursor = next
		}
		seenIDs := map[string]bool{}
		unique := ids[:0]
		for _, id := range ids {
			if !seenIDs[id] {
				unique = append(unique, id)
				seenIDs[id] = true
			}
		}
		ids = unique

		for _, id := range ids {
			if !ValidModelID(id) {
				return nil, errors.New("上游模型 ID 格式无效")
			}
			models = append(models, GatewayModel{UpstreamID: id, Protocols: append([]string(nil), g.Protocols...)})
		}
	}

	// Explicit rows completely replace the defaults for that upstream model.
	// Legacy scalar-protocol rows remain accepted and retain their public IDs.
	overridden := map[string]bool{}
	for _, m := range g.Models {
		overridden[m.UpstreamID] = true
	}
	filtered := models[:0]
	for _, m := range models {
		if !overridden[m.UpstreamID] {
			filtered = append(filtered, m)
		}
	}
	models = append(filtered, g.Models...)
	return models, nil
}
func GatewayProvider(account int64, protocol string) string {
	return modelref.GatewayProvider(account, protocol)
}
func ModelProtocol(provider string) string { return modelref.Protocol(provider) }
func NextRelayChain(chain, id string) (string, error) {
	if !regexp.MustCompile(`^[0-9a-f]{32}$`).MatchString(id) {
		return "", errors.New("当前网关实例标识无效")
	}
	if len(chain) > 256 {
		return "", errors.New("网关转发链过长")
	}
	ids := []string{}
	if chain != "" {
		ids = strings.Split(chain, ",")
	}
	if len(ids) >= RelayHopLimit {
		return "", errors.New("网关转发超过最大跳数")
	}
	for _, value := range ids {
		if len(value) != 32 || !regexp.MustCompile(`^[0-9a-f]{32}$`).MatchString(value) {
			return "", errors.New("网关转发链格式无效")
		}
		if value == id {
			return "", errors.New("检测到网关循环转发")
		}
	}
	return strings.Join(append(ids, id), ","), nil
}
