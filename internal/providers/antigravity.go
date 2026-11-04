package providers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// The retry structure and request metadata are based on the local sub2api
// implementation documented in docs/upstream-migration.md. Storage and account
// scheduling remain separate from this development probe.
const defaultAntigravityVersion = "2.23.0"

// ProjectPendingError means OAuth succeeded but account initialization is not
// ready for model use. It must never be interpreted as an invalid OAuth grant.
type ProjectPendingError struct {
	Attempts int
	Cause    error
}

func (e *ProjectPendingError) Error() string {
	return fmt.Sprintf("OAuth 授权成功，项目待补全（已尝试 %d 轮）：%v", e.Attempts, e.Cause)
}
func (e *ProjectPendingError) Unwrap() error { return e.Cause }

type antigravityLoad struct {
	Project      json.RawMessage `json:"cloudaicompanionProject"`
	Error        json.RawMessage `json:"error"`
	AllowedTiers []struct {
		ID      string `json:"id"`
		Default bool   `json:"isDefault"`
	} `json:"allowedTiers"`
}

type retryWait func(context.Context, time.Duration) error

func waitAntigravity(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func antigravityVersion(c Config) string {
	if c.AntigravityVersion != "" {
		return c.AntigravityVersion
	}
	return defaultAntigravityVersion
}

func antigravityHeaders(req *http.Request, c Config, t Token) {
	req.Header.Set("User-Agent", "antigravity/hub/"+antigravityVersion(c)+" windows/amd64")
	for k, v := range c.ModelsHeaders {
		req.Header.Set(k, v)
	}
	req.Header.Set("Authorization", "Bearer "+t.AccessToken)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
}

func projectID(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return strings.TrimSpace(s)
	}
	var object struct {
		ID string `json:"id"`
	}
	if json.Unmarshal(raw, &object) == nil {
		return strings.TrimSpace(object.ID)
	}
	return ""
}

func antigravityPost(ctx context.Context, client *http.Client, c Config, t Token, path string, body []byte) ([]byte, error) {
	u, err := url.Parse(c.ModelsURL)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.Fragment != "" {
		return nil, errors.New("Antigravity 端点无效")
	}
	origins := []string{"https://" + u.Host}
	fallbacks := c.AntigravityFallbackURLs
	// Only known official origins receive an automatic official fallback. Custom
	// endpoints never cause tests/private traffic to escape to a real service.
	if len(fallbacks) == 0 && u.Host == "cloudcode-pa.googleapis.com" {
		fallbacks = []string{"https://daily-cloudcode-pa.googleapis.com"}
	}
	if len(fallbacks) == 0 && u.Host == "daily-cloudcode-pa.googleapis.com" {
		fallbacks = []string{"https://cloudcode-pa.googleapis.com"}
	}
	origins = append(origins, fallbacks...)
	var last error
	for _, origin := range origins {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		endpoint, e := url.Parse(origin)
		if e != nil || endpoint.Scheme != "https" || endpoint.Host == "" || endpoint.User != nil || endpoint.RawQuery != "" || endpoint.Fragment != "" || (endpoint.Path != "" && endpoint.Path != "/") {
			return nil, errors.New("Antigravity 备用端点无效")
		}
		u.Scheme = endpoint.Scheme
		u.Host = endpoint.Host
		u.Path = path
		u.RawPath = ""
		u.RawQuery = ""
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, u.String(), bytes.NewReader(body))
		if err != nil {
			return nil, errors.New("无法构造 Antigravity 请求")
		}
		antigravityHeaders(req, c, t)
		data, err := perform(client, req)
		if err == nil {
			return data, nil
		}
		last = err
		if !antigravityFallbackAllowed(err) {
			return nil, err
		}
	}
	return nil, last
}

func antigravityFallbackAllowed(err error) bool {
	var network *upstreamNetworkError
	if errors.As(err, &network) {
		return true
	} // The caller checks cancellation before trying another endpoint.
	var status *UpstreamHTTPError
	if errors.As(err, &status) {
		return status.Status == 408 || status.Status == 404 || status.Status == 429 || status.Status >= 500
	}
	return false
}

func resolveAntigravityProject(ctx context.Context, client *http.Client, c Config, t Token) (Config, error) {
	return resolveAntigravityProjectWithWait(ctx, client, c, t, waitAntigravity)
}

// PrepareAntigravity exposes the tested project recovery behavior to the first
// account adapter. It does not persist credentials or declare inference access.
func PrepareAntigravity(ctx context.Context, client *http.Client, c Config, t Token) (Config, error) {
	return resolveAntigravityProject(ctx, client, c, t)
}

func resolveAntigravityProjectWithWait(ctx context.Context, client *http.Client, c Config, t Token, wait retryWait) (Config, error) {
	var body map[string]json.RawMessage
	if len(c.ModelsBody) > 0 && json.Unmarshal(c.ModelsBody, &body) != nil {
		return c, errors.New("Antigravity 模型请求 JSON 无效")
	}
	if body == nil {
		body = map[string]json.RawMessage{}
	}
	var existing string
	if raw, ok := body["project"]; ok && json.Unmarshal(raw, &existing) != nil {
		return c, errors.New("Antigravity project 必须为字符串")
	}
	if existing = strings.TrimSpace(existing); existing != "" && existing != "REPLACE_WITH_PROJECT_ID" {
		return c, nil
	}

	metadata, _ := json.Marshal(map[string]any{"metadata": map[string]string{"ideType": "ANTIGRAVITY", "ideName": "antigravity", "ideVersion": antigravityVersion(c)}})
	var last error
	// Match the reference service: initial attempt plus 3 retries, 1/2/4s.
	for attempt := 0; attempt < 4; attempt++ {
		if err := ctx.Err(); err != nil {
			return c, err
		}
		if attempt > 0 {
			if err := wait(ctx, time.Second*time.Duration(1<<(attempt-1))); err != nil {
				return c, err
			}
		}
		data, err := antigravityPost(ctx, client, c, t, "/v1internal:loadCodeAssist", metadata)
		if err != nil {
			last = fmt.Errorf("项目发现失败：%w", err)
			continue
		}
		var load antigravityLoad
		if json.Unmarshal(data, &load) != nil || len(load.Error) > 0 {
			last = errors.New("项目发现返回无效响应")
			continue
		}
		project := projectID(load.Project)
		if project == "" && c.AllowOnboard {
			tier := ""
			for _, item := range load.AllowedTiers {
				if item.Default && strings.TrimSpace(item.ID) != "" {
					tier = strings.TrimSpace(item.ID)
					break
				}
			}
			if tier == "" {
				last = errors.New("未返回可用默认 tier，未执行 onboardUser")
				continue
			}
			project, err = onboardAntigravityWithWait(ctx, client, c, t, tier, wait)
			if err != nil {
				last = err
				continue
			}
		}
		if project == "" {
			last = errors.New("未返回 project ID；可使用 --onboard 按默认 tier 初始化")
			continue
		}
		body["project"], _ = json.Marshal(project)
		c.ModelsBody, _ = json.Marshal(body)
		c.ModelsMethod = http.MethodPost
		return c, nil
	}
	return c, &ProjectPendingError{Attempts: 4, Cause: last}
}

func onboardAntigravityWithWait(ctx context.Context, client *http.Client, c Config, t Token, tier string, wait retryWait) (string, error) {
	body, _ := json.Marshal(map[string]any{"tierId": tier, "metadata": map[string]string{"ideType": "ANTIGRAVITY", "platform": "PLATFORM_UNSPECIFIED", "pluginType": "GEMINI"}})
	for attempt := 0; attempt < 5; attempt++ {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		data, err := antigravityPost(ctx, client, c, t, "/v1internal:onboardUser", body)
		if err != nil {
			return "", fmt.Errorf("onboardUser 失败：%w", err)
		}
		var response struct {
			Done     *bool           `json:"done"`
			Error    json.RawMessage `json:"error"`
			Response struct {
				Project json.RawMessage `json:"cloudaicompanionProject"`
			} `json:"response"`
		}
		if json.Unmarshal(data, &response) != nil || response.Done == nil || len(response.Error) > 0 {
			return "", errors.New("onboardUser 返回无效响应")
		}
		if *response.Done {
			if project := projectID(response.Response.Project); project != "" {
				return project, nil
			}
			return "", errors.New("初始化完成但未返回 project ID，将重新发现项目")
		}
		if attempt < 4 {
			if err := wait(ctx, 2*time.Second); err != nil {
				return "", err
			}
		}
	}
	return "", errors.New("onboardUser 尚未完成（已尝试 5 次）")
}
