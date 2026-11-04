package providers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"sort"
)

func Models(ctx context.Context, client *http.Client, c Config, t Token) ([]string, error) {
	c = NormalizeOpenAIConfig(c)
	if c.Provider == "antigravity" {
		var err error
		c, err = resolveAntigravityProject(ctx, client, c, t)
		if err != nil {
			return nil, err
		}
		data, err := antigravityPost(ctx, client, c, t, "/v1internal:fetchAvailableModels", c.ModelsBody)
		if err != nil {
			return nil, err
		}
		return ParseModels(data)
	}
	method := c.ModelsMethod
	if method == "" {
		method = "GET"
	}
	req, err := http.NewRequestWithContext(ctx, method, c.ModelsURL, bytes.NewReader(c.ModelsBody))
	if err != nil {
		return nil, errors.New("无法构造模型目录请求")
	}
	IdentityHeaders(req.Header, c)
	for k, v := range c.ModelsHeaders {
		req.Header.Set(k, v)
	}
	if c.Provider == "openai" {
		SetOpenAIAccountHeader(req.Header, c, t)
	}
	req.Header.Set("Authorization", "Bearer "+t.AccessToken)
	req.Header.Set("Accept", "application/json")
	if len(c.ModelsBody) > 0 {
		req.Header.Set("Content-Type", "application/json")
	}
	b, err := perform(client, req)
	if err != nil {
		return nil, err
	}
	return ParseModels(b)
}

// ParseModels supports Anthropic/OpenAI data arrays, Codex slug arrays,
// and Antigravity's model-ID-keyed map. It rejects absent/malformed catalogs.
func ParseModels(b []byte) ([]string, error) {
	var root map[string]json.RawMessage
	if json.Unmarshal(b, &root) != nil || root == nil {
		return nil, errors.New("无效模型目录 JSON")
	}
	if _, ok := root["error"]; ok {
		return nil, errors.New("模型目录返回错误对象")
	}
	raw, ok := root["data"]
	standard := ok
	if !ok {
		raw, ok = root["models"]
	}
	if !ok {
		return nil, errors.New("模型目录缺少 data/models")
	}
	raw = bytes.TrimSpace(raw)
	ids := map[string]bool{}
	if len(raw) > 0 && raw[0] == '[' {
		var entries []struct {
			ID   string `json:"id"`
			Slug string `json:"slug"`
		}
		if json.Unmarshal(raw, &entries) != nil {
			return nil, errors.New("模型数组格式错误")
		}
		for _, e := range entries {
			id := e.ID
			if id == "" {
				id = e.Slug
			}
			if id == "" {
				return nil, errors.New("模型缺少 id/slug")
			}
			ids[id] = true
		}
	} else if !standard && len(raw) > 0 && raw[0] == '{' {
		var entries map[string]json.RawMessage
		if json.Unmarshal(raw, &entries) != nil {
			return nil, errors.New("模型映射格式错误")
		}
		for id, v := range entries {
			v = bytes.TrimSpace(v)
			if id == "" || len(v) == 0 || v[0] != '{' {
				return nil, errors.New("模型映射条目格式错误")
			}
			ids[id] = true
		}
	} else {
		return nil, errors.New("模型目录必须为数组或对象")
	}
	result := make([]string, 0, len(ids))
	for id := range ids {
		result = append(result, id)
	}
	sort.Strings(result)
	return result, nil
}
