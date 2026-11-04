package gateway

import (
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Only enumerated signal names leave the protocol layer. Provider messages and
// arbitrary request/response text must never become account diagnostics.
func errorSignal(raw []byte) string {
	var root struct {
		Error struct {
			Code   string `json:"code"`
			Type   string `json:"type"`
			Status string `json:"status"`
		} `json:"error"`
		Response struct {
			Error struct {
				Code string `json:"code"`
				Type string `json:"type"`
			} `json:"error"`
		} `json:"response"`
	}
	if json.Unmarshal(raw, &root) != nil {
		return ""
	}
	for _, code := range []string{root.Error.Code, root.Error.Type, root.Error.Status, root.Response.Error.Code, root.Response.Error.Type} {
		switch code {
		case "rate_limit_exceeded", "usage_limit_reached", "quota_exceeded", "insufficient_quota", "RESOURCE_EXHAUSTED":
			return "rate_limit"
		case "overloaded_error", "server_is_overloaded", "server_overloaded":
			return "overloaded"
		case "model_not_found", "model_not_supported", "unsupported_model", "model_access_denied":
			return "model_unavailable"
		case "previous_response_not_found":
			return "context_missing"
		case "invalid_api_key", "invalid_token", "authentication_error", "UNAUTHENTICATED", "account_deactivated":
			return "authentication"
		}
	}
	return ""
}
func validPercent(raw string) *float64 {
	v, err := strconv.ParseFloat(strings.TrimSpace(raw), 64)
	if err != nil || v < 0 || v > 100 || v != v {
		return nil
	}
	return &v
}
func resetTime(h http.Header, prefix string, now time.Time) *int64 {
	if raw := h.Get(prefix + "-reset-after-seconds"); raw != "" {
		v, e := strconv.ParseFloat(raw, 64)
		if e == nil && v >= 0 && v <= 30*86400 {
			n := now.Add(time.Duration(v * float64(time.Second))).Unix()
			return &n
		}
	}
	if raw := h.Get(prefix + "-reset-at"); raw != "" {
		v, e := strconv.ParseInt(raw, 10, 64)
		if e == nil && v >= now.Unix() && v <= now.Add(30*24*time.Hour).Unix() {
			return &v
		}
	}
	return nil
}
func observeHeaders(out *result, h http.Header) {
	now := time.Now()
	out.primaryUsed = validPercent(h.Get("x-codex-primary-used-percent"))
	out.secondaryUsed = validPercent(h.Get("x-codex-secondary-used-percent"))
	out.primaryReset = resetTime(h, "x-codex-primary", now)
	out.secondaryReset = resetTime(h, "x-codex-secondary", now)
	for _, pair := range []struct {
		used  *float64
		reset *int64
	}{{out.primaryUsed, out.primaryReset}, {out.secondaryUsed, out.secondaryReset}} {
		if pair.used != nil && *pair.used >= 100 && pair.reset != nil && *pair.reset > out.cooldownUntil {
			out.cooldownUntil = *pair.reset
			out.signal = "quota_exhausted"
		}
	}
	if h.Get("Retry-After") != "" {
		if seconds, e := strconv.ParseFloat(h.Get("Retry-After"), 64); e == nil && seconds >= 0 && seconds <= 30*86400 {
			out.retryAfter = now.Add(time.Duration(seconds * float64(time.Second))).Unix()
		} else if at, e := http.ParseTime(h.Get("Retry-After")); e == nil && at.After(now) && at.Before(now.Add(30*24*time.Hour)) {
			out.retryAfter = at.Unix()
		}
	}
}
func classifyRejection(out *result, resp *http.Response) {
	observeHeaders(out, resp.Header)
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	out.signal = errorSignal(raw)
	out.retryAfter = max(out.retryAfter, googleRetryHint(raw))
	if out.status == 429 {
		out.signal = "rate_limit"
	}
	if out.status == 401 {
		out.signal = "authentication"
	}
	switch out.signal {
	case "rate_limit":
		out.retryable = true
		out.known = true
		out.complete = true
		out.cooldownUntil = max(out.cooldownUntil, out.retryAfter)
		if out.cooldownUntil <= time.Now().Unix() {
			out.cooldownUntil = time.Now().Add(time.Minute).Unix()
		}
	case "overloaded":
		out.retryable = true
		out.known = true
		out.complete = true
		out.cooldownUntil = max(out.retryAfter, time.Now().Add(5*time.Second).Unix())
	case "model_unavailable":
		out.retryable = true
		out.known = true
		out.complete = true
		out.modelCooldown = true
		out.cooldownUntil = time.Now().Add(5 * time.Minute).Unix()
	case "authentication":
		out.retryable = true
		out.known = true
		out.complete = true
		out.cooldownUntil = time.Now().Add(5 * time.Minute).Unix()
	default:
		if out.status == 403 {
			out.retryable = true
			out.cooldownUntil = time.Now().Add(time.Minute).Unix()
			out.modelCooldown = true
			out.signal = "permission_denied"
		}
	}
}
func applyStreamSignal(out *result, raw []byte) {
	signal := errorSignal(raw)
	out.retryAfter = max(out.retryAfter, googleRetryHint(raw))
	if signal == "" {
		return
	}
	out.signal = signal
	if signal == "context_missing" {
		out.status = 400
		out.known = true
		return
	}
	// A reported non-zero usage is evidence of execution. Never replay it.
	used := out.usage.Input + out.usage.Output + out.usage.Cached + out.usage.CacheWrite
	if used > 0 || out.invalidUsage {
		return
	}
	if signal == "rate_limit" || signal == "overloaded" {
		out.retryable = true
		out.known = true
		out.complete = true
		delay := 5 * time.Second
		if signal == "rate_limit" {
			delay = time.Minute
		}
		out.cooldownUntil = max(out.cooldownUntil, out.retryAfter, time.Now().Add(delay).Unix())
	}
}

// Google RetryInfo is an explicit retry hint, not free-form message matching.
func googleRetryHint(raw []byte) int64 {
	var body struct {
		Error struct {
			Details []struct {
				Type  string `json:"@type"`
				Delay string `json:"retryDelay"`
			} `json:"details"`
		} `json:"error"`
	}
	if json.Unmarshal(raw, &body) != nil {
		return 0
	}
	until := int64(0)
	for _, detail := range body.Error.Details {
		if detail.Type != "type.googleapis.com/google.rpc.RetryInfo" || len(detail.Delay) > 32 {
			continue
		}
		delay, e := time.ParseDuration(detail.Delay)
		if e == nil && delay > 0 && delay <= 30*24*time.Hour {
			until = max(until, time.Now().Add(delay).Unix())
		}
	}
	return until
}
