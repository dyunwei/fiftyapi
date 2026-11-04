package gateway

import (
	"encoding/json"
	"errors"
	"fiftyapi/internal/providers"
	"fiftyapi/internal/store"
	"io"
	"net/http"
	"net/url"
)

func (h *Handler) codexManifest(w http.ResponseWriter, r *http.Request, models []store.Model) error {
	version := r.URL.Query().Get("client_version")
	if !providers.ValidClientVersion(version) {
		return errors.New("Codex 客户端版本无效")
	}
	allowed := map[string]string{}
	var ids []int64
	source := ""
	// Prefer native Codex metadata when present. Otherwise use one configured
	// Responses gateway; never merge catalogs from unrelated gateway accounts.
	for _, native := range []bool{true, false} {
		for _, m := range models {
			if !m.Enabled || m.Price == nil || !m.Available || !m.SupportsProtocol("responses") || (native != (m.Provider == "openai")) {
				continue
			}
			_, candidates, e := h.store.RoutableModel(r.Context(), m.ID)
			if e == nil {
				source = m.Provider
				ids = candidates
				break
			}
		}
		if len(ids) > 0 {
			break
		}
	}
	if len(ids) == 0 {
		return errors.New("没有已定价的 Responses 模型或可用账号")
	}
	for _, m := range models {
		if m.Provider == source && m.Enabled && m.Price != nil && m.Available {
			allowed[m.UpstreamID] = m.ID
		}
	}
	c, err := h.accounts.Access(r.Context(), ids[0])
	if err != nil {
		return err
	}
	endpoint := c.Config.ModelsURL
	if c.Config.Provider == "gateway" {
		endpoint, err = providers.GatewayEndpoint(*c.Config.Gateway, "models")
		if err != nil {
			return err
		}
	}
	u, err := url.Parse(endpoint)
	if err != nil {
		return err
	}
	query := u.Query()
	query.Set("client_version", version)
	u.RawQuery = query.Encode()
	req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, u.String(), nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	if c.Config.Provider == "gateway" {
		providers.GatewayAuth(req.Header, c.Config, c.APIKey)
		chain, e := providers.NextRelayChain(r.Header.Get(providers.RelayChainHeader), h.instanceID)
		if e != nil {
			return e
		}
		req.Header.Set(providers.RelayChainHeader, chain)
		if sameOriginHost(endpoint, r.Host, func() string {
			if r.TLS != nil {
				return "https"
			}
			return "http"
		}()) {
			return errors.New("模型目录指向当前实例，已阻止循环转发")
		}
	} else {
		req.Header.Set("Authorization", "Bearer "+c.Token.AccessToken)
		manifestConfig := c.Config
		manifestConfig.ClientVersion = version
		providers.IdentityHeaders(req.Header, manifestConfig)
		providers.SetOpenAIAccountHeader(req.Header, c.Config, c.Token)
	}
	for k, v := range c.Config.ModelsHeaders {
		req.Header.Set(k, v)
	}
	client := h.client(c.Config)
	defer client.CloseIdleConnections()
	resp, err := client.Do(req)
	if err != nil {
		return errors.New("无法获取 Codex 模型目录")
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return errors.New("上游拒绝模型目录请求")
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil || len(b) >= 8<<20 {
		return errors.New("模型目录过大或读取失败")
	}
	var catalog map[string]json.RawMessage
	if json.Unmarshal(b, &catalog) != nil {
		return errors.New("模型目录格式错误")
	}
	var entries []map[string]json.RawMessage
	if json.Unmarshal(catalog["models"], &entries) != nil {
		return errors.New("上游未返回 Codex 模型元数据；通用模型目录不能代替 Codex manifest")
	}
	filtered := []map[string]json.RawMessage{}
	for _, entry := range entries {
		var slug string
		json.Unmarshal(entry["slug"], &slug)
		if id, ok := allowed[slug]; ok {
			entry["slug"], _ = json.Marshal(id)
			filtered = append(filtered, entry)
		}
	}
	catalog["models"], _ = json.Marshal(filtered)
	w.Header().Set("Content-Type", "application/json")
	return json.NewEncoder(w).Encode(catalog)
}
