package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"fiftyapi/internal/store"
	"net/http"
	"strings"
	"time"
)

type ProbeResult struct {
	Request    json.RawMessage `json:"request"`
	Response   string          `json:"response"`
	Status     int             `json:"status"`
	DurationMS int64           `json:"duration_ms"`
	Error      string          `json:"error,omitempty"`
	Truncated  bool            `json:"truncated"`
}

// probeWriter bounds the diagnostic retained in memory while still draining upstream.
type probeWriter struct {
	header    http.Header
	body      strings.Builder
	truncated bool
}

func (w *probeWriter) Header() http.Header { return w.header }
func (w *probeWriter) WriteHeader(int)     {}
func (w *probeWriter) Flush()              {}
func (w *probeWriter) Write(p []byte) (int, error) {
	n := len(p)
	remaining := (256 << 10) - w.body.Len()
	if len(p) > remaining {
		p = p[:remaining]
		w.truncated = true
	}
	w.body.Write(p)
	return n, nil
}

// Probe bypasses local pricing and billing, but uses the account's real adapter and concurrency limit.
func (h *Handler) Probe(ctx context.Context, account int64, model, protocol, host string) (ProbeResult, error) {
	var out ProbeResult
	selected, err := h.store.AccountModel(ctx, account, model)
	if err != nil {
		return out, errors.New("该模型不属于此账号或账号不存在")
	}
	supported := false
	for _, p := range selected.Protocols {
		if p == protocol {
			supported = true
		}
	}
	if !supported {
		return out, errors.New("该模型不支持所选协议")
	}
	ctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	c, err := h.accounts.Access(ctx, account)
	if err != nil {
		return out, errors.New("账号凭据不可用，请验证或重新授权")
	}
	policy, err := h.store.AccountPolicy(ctx, account)
	if err != nil {
		return out, err
	}
	slot := h.slot(account)
	if !slot.acquire(policy.Concurrency) {
		return out, errors.New("账号忙，请稍后测试")
	}
	defer slot.release()
	body := map[string]any{"model": selected.UpstreamID, "stream": false}
	path := "/v1/" + protocol
	if protocol == "responses" {
		body["input"] = "hi"
		body["instructions"] = "Reply briefly to the greeting."
	} else {
		body["messages"] = []any{map[string]string{"role": "user", "content": "hi"}}
		if protocol == "messages" {
			body["max_tokens"] = 64
		} else {
			path = "/v1/chat/completions"
		}
	}
	raw, _ := json.Marshal(body)
	out.Request = raw
	request := map[string]json.RawMessage{}
	_ = json.Unmarshal(raw, &request)
	r, _ := http.NewRequestWithContext(ctx, "POST", "http://"+host+path, strings.NewReader(string(raw)))
	w := &probeWriter{header: make(http.Header)}
	start := time.Now()
	result := h.forward(w, r, raw, request, store.Model{ID: selected.ID, UpstreamID: selected.UpstreamID, Provider: c.Config.Provider, Protocol: protocol}, c, 64)
	out.DurationMS = time.Since(start).Milliseconds()
	out.Status = result.status
	out.Response = w.body.String()
	out.Truncated = w.truncated
	if result.err != nil {
		out.Error = "上游测试失败：" + result.err.Error()
	}
	return out, nil
}
