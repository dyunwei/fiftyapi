package gateway

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fiftyapi/internal/accounts"
	"fiftyapi/internal/providers"
	"fiftyapi/internal/store"
	"io"
	"log"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

type Handler struct {
	store        *store.Store
	accounts     *accounts.Service
	counter      atomic.Uint64
	instanceID   string
	slots        sync.Map
	wsMu         sync.Mutex
	wsClosing    bool
	wsCancels    map[uint64]context.CancelFunc
	wsWG         sync.WaitGroup
	wsID         atomic.Uint64
	wsUsers      sync.Map
	wsTotal      atomic.Int32
	wsQueueBytes atomic.Int64
	cooldowns    sync.Map
	client       func(providers.Config) *http.Client
}

func New(s *store.Store, a *accounts.Service) *Handler {
	id, _ := s.RelayInstanceID(context.Background())
	return &Handler{instanceID: id, store: s, accounts: a, client: providers.HTTPClient}
}
func failure(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	payload := map[string]any{"type": "error", "error": map[string]string{"type": "gateway_error", "message": message}}
	if id := w.Header().Get("X-Request-ID"); id != "" {
		payload["request_id"] = id
	}
	json.NewEncoder(w).Encode(payload)
}
func (h *Handler) auth(r *http.Request) (store.User, int64, error) {
	token := ""
	if authorization := r.Header.Get("Authorization"); authorization != "" {
		fields := strings.Fields(authorization)
		if len(fields) != 2 || !strings.EqualFold(fields[0], "Bearer") {
			return store.User{}, 0, errors.New("认证格式无效")
		}
		token = fields[1]
	} else {
		token = r.Header.Get("x-api-key")
	}

	if token == "" {
		return store.User{}, 0, errors.New("缺少 API Key")
	}
	return h.store.AuthenticateAPIKey(r.Context(), token)
}
func (h *Handler) Models(w http.ResponseWriter, r *http.Request) {
	if _, _, err := h.auth(r); err != nil {
		failure(w, 401, "API Key 无效")
		return
	}
	if _, err := providers.NextRelayChain(r.Header.Get(providers.RelayChainHeader), h.instanceID); err != nil {
		failure(w, 508, err.Error())
		return
	}
	models, err := h.store.Models(r.Context())
	if err != nil {
		failure(w, 500, "读取模型失败")
		return
	}
	if r.URL.Query().Get("client_version") != "" {
		if err = h.codexManifest(w, r, models); err != nil {
			failure(w, 502, err.Error())
		}
		return
	}
	list := []any{}
	for _, m := range models {
		if m.Enabled && m.Price != nil && m.Available {
			endpoints, endpointTypes := []string{}, []string{}
			for _, protocol := range m.Protocols {
				switch protocol {
				case "chat":
					endpoints = append(endpoints, "/v1/chat/completions")
					endpointTypes = append(endpointTypes, "openai")
				case "responses":
					endpoints = append(endpoints, "/v1/responses")
					endpointTypes = append(endpointTypes, "openai-response")
				case "messages":
					endpoints = append(endpoints, "/v1/messages")
					endpointTypes = append(endpointTypes, "anthropic")
				}
			}
			list = append(list, map[string]any{"id": m.ID, "object": "model", "type": "model", "display_name": m.ID, "owned_by": m.Provider, "protocol": m.Protocol, "protocols": m.Protocols, "supported_endpoint_types": endpointTypes, "supported_endpoints": endpoints})
		}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"object": "list", "data": list, "has_more": false})
}

type result struct {
	usage                        store.TokenUsage
	known                        bool
	complete                     bool
	status                       int
	err                          error
	sent                         bool
	dispatched                   bool
	seenInput                    bool
	seenOutput                   bool
	invalidUsage                 bool
	inputTotal                   int64
	retryable                    bool
	signal                       string
	cooldownUntil, retryAfter    int64
	modelCooldown                bool
	primaryUsed, secondaryUsed   *float64
	primaryReset, secondaryReset *int64
}

func (h *Handler) Serve(w http.ResponseWriter, r *http.Request) {
	defer http.NewResponseController(w).SetWriteDeadline(time.Time{})
	start := time.Now()
	user, key, err := h.auth(r)
	if err != nil {
		failure(w, 401, "API Key 无效或账号已停用")
		return
	}
	nonce := make([]byte, 24)
	if _, err = rand.Read(nonce); err != nil {
		failure(w, 500, "无法创建请求记录")
		return
	}
	id := base64.RawURLEncoding.EncodeToString(nonce)
	w.Header().Set("X-Request-ID", id)
	recorded := false
	logModel, logProvider := "", ""
	var logInference store.RequestInference
	tracked := &statusWriter{ResponseWriter: w}
	w = tracked
	defer func() {
		if !recorded && !strings.HasSuffix(r.URL.Path, "count_tokens") {
			ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 3*time.Second)
			defer cancel()
			zero := int64(0)
			status := tracked.status
			if status == 0 {
				status = 500
			}
			h.store.LogRequest(ctx, store.RequestLog{RequestInference: logInference, ID: id, UserID: user.ID, KeyID: key, Model: logModel, Provider: logProvider, Status: status, Amount: &zero, DurationMS: time.Since(start).Milliseconds(), Error: "请求未发送到上游", CreatedAt: start.Unix()})
		}
	}()
	if _, err := providers.NextRelayChain(r.Header.Get(providers.RelayChainHeader), h.instanceID); err != nil {
		failure(w, 508, err.Error())
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 4<<20))
	if err != nil {
		failure(w, 413, "请求超过 4 MiB 限制")
		return
	}
	var request map[string]json.RawMessage
	if json.Unmarshal(body, &request) != nil {
		failure(w, 400, "请求 JSON 无效")
		return
	}
	requestedProtocol := "messages"
	if strings.Contains(r.URL.Path, "responses") {
		requestedProtocol = "responses"
	} else if strings.HasSuffix(r.URL.Path, "/chat/completions") {
		requestedProtocol = "chat"
	}
	logInference = requestInference(request, requestedProtocol)
	if raw := request["stream_id"]; len(raw) > 0 && r.Context().Value(websocketStateKey{}) == nil {
		failure(w, 400, "stream_id 仅用于 Responses WebSocket")
		return
	}
	var modelName string
	if json.Unmarshal(request["model"], &modelName) != nil || modelName == "" {
		failure(w, 400, "缺少模型 ID")
		return
	}
	m, _, err := h.store.RoutableModel(r.Context(), modelName)
	logModel = m.ID
	logProvider = m.Provider
	if err != nil {
		failure(w, 400, "模型未启用、未定价或没有可用账号")
		return
	}
	isResponses := requestedProtocol == "responses"
	if isResponses {
		if raw := request["previous_response_id"]; len(raw) > 0 && string(raw) != "null" && string(raw) != `""` && r.Context().Value(websocketStateKey{}) == nil {
			failure(w, 400, "此网关使用无状态 Responses，请回传完整上下文")
			return
		}
	}
	if !m.SupportsProtocol(requestedProtocol) {
		failure(w, 400, "协议不匹配：所选模型不支持当前接口；请按模型支持的协议配置客户端，本服务不自动转换协议")
		return
	}
	m.Protocol = requestedProtocol
	r = r.WithContext(context.WithValue(r.Context(), requestProtocolKey{}, requestedProtocol))
	// count_tokens is an explicit local estimate and never opens a billing window.
	if strings.HasSuffix(r.URL.Path, "count_tokens") {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"input_tokens": (len(body) + 2) / 3, "estimated": true})
		return
	}
	var output int64
	field := "max_tokens"
	if requestedProtocol == "chat" && len(request["max_completion_tokens"]) > 0 {
		field = "max_completion_tokens"
	}
	if isResponses {
		field = "max_output_tokens"
	}
	if raw := request[field]; len(raw) > 0 {
		if json.Unmarshal(raw, &output) != nil {
			failure(w, 400, "输出上限必须是整数")
			return
		}
	}
	if output <= 0 {
		output = 8192
	}
	if output > 65536 {
		failure(w, 400, "输出上限不能超过 65536 Token")
		return
	}
	estimatePrice := *m.Price
	estimatePrice.Input = max(estimatePrice.Input, estimatePrice.CacheWrite, estimatePrice.Cached)
	candidates := int64(1)
	if requestedProtocol == "chat" {
		if raw := request["n"]; len(raw) > 0 && string(raw) != "null" {
			if json.Unmarshal(raw, &candidates) != nil || candidates < 1 || candidates > 128 {
				failure(w, 400, "n 必须为 1–128 的整数")
				return
			}
		}
	}
	estimate, err := estimatePrice.Cost(store.TokenUsage{Input: (int64(len(body)) + 4096) * candidates, Output: output * candidates})
	if err != nil {
		failure(w, 400, "无法预估额度")
		return
	}
	estimate = max(estimate, 1)
	if err = h.store.Reserve(r.Context(), id, user.ID, key, m.ID, *m.Price, estimate, start); err != nil {
		status := 503
		if errors.Is(err, store.ErrQuota) {
			status = 429
		}
		failure(w, status, err.Error())
		return
	}
	recorded = true
	w.Header().Set("X-Request-ID", id)
	logEntry := store.RequestLog{RequestInference: logInference, ID: id, UserID: user.ID, KeyID: key, Model: m.ID, Provider: m.Provider, CreatedAt: start.Unix()}
	finish := func(out result) {
		saveCtx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 5*time.Second)
		defer cancel()
		logEntry.Status = out.status
		logEntry.DurationMS = time.Since(start).Milliseconds()
		if out.known && !out.invalidUsage {
			logEntry.Input = &out.usage.Input
			logEntry.Output = &out.usage.Output
			logEntry.Cached = &out.usage.Cached
			logEntry.CacheWrite = &out.usage.CacheWrite
		}
		if out.err != nil {
			logEntry.Error = out.err.Error()
		}
		if !out.dispatched {
			logEntry.Error = "请求未发送到上游：" + logEntry.Error
		}
		if err := h.store.FinalizeRequest(saveCtx, logEntry); err != nil {
			log.Printf("请求 %s 自动结算或日志持久化失败，请检查待核算请求", id)
		}
	}

	// Affinity belongs to the logical model pool and protocol, not to whichever
	// provider happened to create the pool first.
	affinityNamespace := m.ID + "\x00" + requestedProtocol
	fingerprint := sessionHash(r, user.ID, affinityNamespace, request)
	portable := historyPortable(request)
	legacy := legacySessionHash(r, user.ID, affinityNamespace, request)
	if len(fingerprint) > 0 && !bytes.Equal(fingerprint, legacy) {
		if _, e := h.store.GatewaySession(r.Context(), fingerprint); e != nil {
			if owner, e := h.store.GatewaySession(r.Context(), legacy); e == nil {
				_, _ = h.store.ClaimGatewaySession(r.Context(), user.ID, owner, fingerprint, 0, false)
			}
		}
	}

	scope := "generate/" + requestedProtocol + ":" + m.ID
	if strings.HasSuffix(r.URL.Path, "/compact") {
		scope = "compact:" + m.ID
	}
	excluded := map[int64]bool{}
	refreshTried := map[int64]bool{}
	out := result{status: 503, err: errors.New("没有可用上游账号")}
	for attempt := 1; attempt <= 3; attempt++ {
		if r.Context().Err() != nil {
			out = result{status: 499, err: errors.New("客户端已取消请求")}
			break
		}
		chosen, e := h.selectAccount(r.Context(), user.ID, m.ID, scope, fingerprint, portable, excluded, id)
		if e != nil {
			if attempt == 1 {
				out = result{status: 503, err: e}
				var unavailable *scheduleUnavailable
				if errors.As(e, &unavailable) {
					out.retryAfter = unavailable.until
					if unavailable.status != 0 {
						out.status = unavailable.status
					}
				}
				if r.Context().Err() != nil {
					out.status = 499
					out.err = errors.New("客户端已取消请求")
				}
			}
			break
		}
		if currentUser, currentKey, e := h.auth(r); e != nil || currentUser.ID != user.ID || currentKey != key {
			chosen.Release()
			out = result{status: 401, known: true, complete: true, err: errors.New("排队期间用户或 API Key 已停用")}
			break
		}
		logEntry.AccountID = &chosen.ID
		logEntry.Provider = chosen.Route.Provider
		started := time.Now()
		attemptRequest := make(map[string]json.RawMessage, len(request))
		for k, v := range request {
			attemptRequest[k] = v
		}
		writer := newAttemptWriter(w)
		attemptModel := m
		attemptModel.Provider = chosen.Route.Provider
		attemptModel.UpstreamID = chosen.Route.UpstreamID
		attemptModel.Protocols = append([]string(nil), chosen.Route.Protocols...)
		out = h.forward(writer, r, body, attemptRequest, attemptModel, chosen.Credential, output)
		// Successful responses may still be buffered (for example, an empty
		// completion). Deliver them before classifying a downstream disconnect.
		if out.complete && out.err == nil && !writer.committed {
			err := writer.commit()
			if err == nil {
				err = writer.FlushError()
			}
			if err != nil {
				out.complete = false
				out.status = 502
				out.err = errors.New("客户端响应写入失败")
			} else {
				out.sent = true
			}
		}
		chosen.Release()
		if r.Context().Err() != nil && !(out.complete && out.err == nil) {
			out.status = 499
			out.err = errors.New("客户端已取消请求")
		}
		refreshed := false
		if out.status == 401 && out.signal == "authentication" && !writer.committed && !refreshTried[chosen.ID] && chosen.Credential.Config.Provider != "gateway" && r.Context().Err() == nil {
			refreshTried[chosen.ID] = true
			if e = h.accounts.RefreshRejected(r.Context(), chosen.ID, chosen.Credential.Token.AccessToken); e == nil {
				refreshed = true
				out.cooldownUntil = 0
				out.signal = "refreshed"
			}
		}
		holdBinding := chosen.Route.Provider == "openai" && out.signal == "rate_limit"
		eligible := !holdBinding && out.retryable && writer.writeErr == nil && !writer.committed && !out.invalidUsage && out.usage.Input+out.usage.Output+out.usage.Cached+out.usage.CacheWrite == 0 && r.Context().Err() == nil
		reason := routingReason(out)
		observation := store.AccountObservation{RequestAttempt: store.RequestAttempt{RequestID: id, Attempt: attempt, AccountID: chosen.ID, Model: m.ID, Status: out.status, DurationMS: time.Since(started).Milliseconds(), Dispatched: out.dispatched, Retryable: eligible && (portable || refreshed) && attempt < 3, Reason: reason, CreatedAt: started.Unix()}, Success: out.complete && out.err == nil, Failure: writer.writeErr == nil && out.err != nil && out.signal != "refreshed" && out.signal != "rate_limit" && out.signal != "quota_exhausted" && out.signal != "model_unavailable" && out.status != 400 && out.status != 403 && r.Context().Err() == nil, CooldownUntil: out.cooldownUntil, HoldBinding: holdBinding, QuotaExhausted: out.signal == "quota_exhausted", PrimaryUsed: out.primaryUsed, PrimaryReset: out.primaryReset, SecondaryUsed: out.secondaryUsed, SecondaryReset: out.secondaryReset}
		if writer.firstOutput != nil {
			ms := writer.firstOutput.Sub(started).Milliseconds()
			observation.FirstOutputMS = &ms
		}
		if out.modelCooldown {
			observation.CooldownScope = scope
		}
		save, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 3*time.Second)
		persistErr := h.store.RecordAttempt(save, observation)
		cancel()
		if persistErr != nil {
			log.Printf("请求 %s 的路由记录保存失败，停止自动重试", id)
			eligible = false
		}
		if out.cooldownUntil > time.Now().Unix() && !out.modelCooldown {
			h.cooldowns.Store(chosen.ID, time.Unix(out.cooldownUntil, 0))
		}
		switchAllowed := true
		if turn, ok := r.Context().Value(websocketStateKey{}).(*websocketTurn); ok && !refreshed {
			switchAllowed = turn.state.canSwitch()
			if eligible && portable && switchAllowed {
				turn.state.close()
			}
		}
		if eligible && (portable || refreshed) && switchAllowed && attempt < 3 {
			if !refreshed {
				excluded[chosen.ID] = true
			}
			out.sent = false
			continue
		}
		if writer.committed || out.err == nil || (!eligible && out.sent) {
			_ = writer.commit()
			writer.Flush()
			out.sent = true
		} else {
			out.sent = false
		}
		break
	}
	if out.err != nil && !out.sent && r.Context().Err() == nil {
		if until := max(out.cooldownUntil, out.retryAfter); until > time.Now().Unix() {
			w.Header().Set("Retry-After", strconv.FormatInt(max(1, until-time.Now().Unix()), 10))
		}

		if tracked.status != 0 {
			payload, _ := json.Marshal(map[string]any{"type": "error", "error": map[string]string{"type": "gateway_error", "message": out.err.Error()}, "request_id": id})
			_ = emitSSE(w, "error", payload)
		} else {
			failure(w, out.status, out.err.Error())
		}
	}
	finish(out)
}

func routingReason(out result) string {
	if out.signal == "quota_exhausted" {
		return "上游额度耗尽，等待窗口重置"
	}
	if out.err == nil {
		return "请求完成"
	}
	switch out.signal {
	case "refreshed":
		return "令牌已刷新，重试原账号"
	case "rate_limit":
		return "上游限流，等待恢复"
	case "quota_exhausted":
		return "上游额度耗尽，等待窗口重置"
	case "overloaded":
		return "上游繁忙，允许安全换号"
	case "authentication":
		return "上游认证失败"
	case "permission_denied":
		return "模型权限拒绝"
	case "model_unavailable":
		return "当前账号不支持此模型"
	}
	if !out.dispatched {
		return "请求未发出"
	}
	return "请求未完成，未自动重放"
}
