package gateway

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fiftyapi/internal/accounts"
	"fiftyapi/internal/providers"
	ws "github.com/coder/websocket"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Responses WebSocket ingress uses a bounded FIFO of turns on one native
// connection. Account affinity lasts for that connection and cached IDs never
// cross users, sockets, or upstream accounts. Multiple client connections run
// concurrently through the same account concurrency/queue limits as HTTP.
func (h *Handler) ServeWebSocket(w http.ResponseWriter, r *http.Request) {
	user, _, err := h.auth(r)
	if err != nil {
		failure(w, 401, "API Key 无效或用户已停用")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Minute)
	defer cancel()
	socketID, ok := h.registerWebSocket(cancel)
	if !ok {
		failure(w, 503, "服务正在停止，请稍后重连")
		return
	}
	defer h.unregisterWebSocket(socketID)
	if _, err = h.store.RelayInstanceID(r.Context()); err != nil {
		failure(w, 503, "网关暂不可用")
		return
	}
	if _, err = providers.NextRelayChain(r.Header.Get(providers.RelayChainHeader), h.instanceID); err != nil {
		failure(w, 508, "检测到网关转发循环")
		return
	}
	value, _ := h.wsUsers.LoadOrStore(user.ID, &atomic.Int32{})
	count := value.(*atomic.Int32)
	if count.Add(1) > 4 {
		count.Add(-1)
		failure(w, 429, "同一用户最多保持 4 个 WebSocket 连接")
		return
	}
	defer count.Add(-1)
	if h.wsTotal.Add(1) > 128 {
		h.wsTotal.Add(-1)
		failure(w, 503, "WebSocket 连接已满")
		return
	}
	defer h.wsTotal.Add(-1)
	conn, err := ws.Accept(w, r, &ws.AcceptOptions{CompressionMode: ws.CompressionDisabled})
	if err != nil {
		return
	}
	defer conn.CloseNow()
	conn.SetReadLimit(4 << 20)
	state := &websocketState{cache: map[string]wsCachedResponse{}}
	defer state.close()
	seed := r.Header.Get("session_id")
	if seed == "" {
		seed = r.Header.Get("x-session-id")
	}
	if seed == "" {
		seed = r.Header.Get("conversation_id")
	}
	if seed == "" {
		nonce := make([]byte, 24)
		if _, err = rand.Read(nonce); err != nil {
			return
		}
		seed = "ws-" + hex.EncodeToString(nonce)
	}
	var writeMu sync.Mutex
	write := func(raw []byte) error {
		writeMu.Lock()
		defer writeMu.Unlock()
		task, stop := context.WithTimeout(ctx, 15*time.Second)
		defer stop()
		return conn.Write(task, ws.MessageText, raw)
	}
	sendError := func(code, message, stream string) {
		raw, _ := json.Marshal(map[string]any{"type": "error", "status": 400, "stream_id": stream, "error": map[string]string{"type": "invalid_request_error", "code": code, "message": message}})
		_ = write(raw)
	}
	queue := make(chan []byte, 16)
	var queuedBytes atomic.Int64
	done := make(chan struct{})
	var activeMu sync.Mutex
	var activeCancel context.CancelFunc
	go func() {
		defer close(done)
		defer func() {
			for {
				select {
				case raw := <-queue:
					h.wsQueueBytes.Add(-int64(len(raw)))
				default:
					return
				}
			}
		}()
		for {
			if ctx.Err() != nil {
				return
			}
			select {
			case <-ctx.Done():
				return
			case raw := <-queue:
				queuedBytes.Add(-int64(len(raw)))
				h.wsQueueBytes.Add(-int64(len(raw)))
				var payload map[string]json.RawMessage
				if json.Unmarshal(raw, &payload) != nil {
					continue
				}
				var model, stream, previous string
				_ = json.Unmarshal(payload["model"], &model)
				_ = json.Unmarshal(payload["stream_id"], &stream)
				_ = json.Unmarshal(payload["previous_response_id"], &previous)
				if previous != "" {
					cached, ok := state.previous(previous)
					if !ok {
						sendError("previous_response_not_found", "当前连接没有该响应上下文，请设置 previous_response_id:null 并发送完整历史", stream)
						continue
					}
					if model == "" {
						model = cached
						payload["model"], _ = json.Marshal(model)
					}
				}
				if model == "" {
					sendError("model_required", "response.create 必须指定模型", stream)
					continue
				}
				// Revalidate every queued turn; revocation is not deferred until reconnect.
				if _, _, e := h.auth(r); e != nil {
					sendError("authentication_error", "API Key 已失效或用户已停用", stream)
					cancel()
					return
				}
				m, _, e := h.store.RoutableModel(ctx, model)
				if e != nil || !m.SupportsProtocol("responses") {
					sendError("model_not_found", "模型未启用、未定价或不支持 Responses", stream)
					continue
				}
				delete(payload, "type")
				payload["stream"] = json.RawMessage("true")
				body, _ := json.Marshal(payload)
				turnCtx, stop := context.WithCancel(ctx)
				activeMu.Lock()
				if ctx.Err() != nil {
					activeMu.Unlock()
					stop()
					return
				}
				activeCancel = stop
				activeMu.Unlock()
				turn := &websocketTurn{state: state, ctx: ctx, model: model, stream: stream, previous: previous}
				turnCtx = context.WithValue(turnCtx, websocketStateKey{}, turn)
				turnCtx = context.WithValue(turnCtx, upstreamClientContextKey{}, upstreamClientFactory(func(c accounts.Credential) *http.Client { return turn.client(h, c) }))
				request := r.Clone(turnCtx)
				request.Method = http.MethodPost
				urlCopy := *r.URL
				urlCopy.Path = "/v1/responses"
				urlCopy.RawQuery = ""
				request.URL = &urlCopy
				request.Header = r.Header.Clone()
				request.Header.Set("Content-Type", "application/json")
				request.Header.Set("session_id", seed)
				request.Body = io.NopCloser(bytes.NewReader(body))
				request.ContentLength = int64(len(body))
				writer := &websocketWriter{header: make(http.Header), write: write, turn: turn}
				h.Serve(writer, request)
				writer.finish()
				activeMu.Lock()
				activeCancel = nil
				activeMu.Unlock()
				stop()
			}
		}
	}()
	defer func() {
		cancel()
		activeMu.Lock()
		if activeCancel != nil {
			activeCancel()
		}
		activeMu.Unlock()
		state.close()
		<-done
	}()
	streams := map[string]bool{}
	for {
		kind, raw, e := conn.Read(ctx)
		if e != nil {
			return
		}
		if kind != ws.MessageText {
			sendError("unsupported_frame", "仅支持 JSON 文本帧", "")
			continue
		}
		var event struct {
			Type   string `json:"type"`
			Stream string `json:"stream_id"`
		}
		if json.Unmarshal(raw, &event) != nil {
			sendError("invalid_json", "请求必须是 JSON 对象", "")
			continue
		}
		if event.Type != "response.create" {
			sendError("unsupported_event", "当前支持 response.create；取消请关闭当前连接", event.Stream)
			continue
		}
		if len(event.Stream) > 256 || strings.IndexFunc(event.Stream, func(r rune) bool {
			return !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_' || r == '-' || r == '.')
		}) >= 0 {
			sendError("invalid_stream_id", "stream_id 格式无效", event.Stream)
			continue
		}
		if event.Stream != "" && !streams[event.Stream] {
			if len(streams) >= 32 {
				sendError("websocket_stream_limit_reached", "当前连接最多使用 32 个命名流", event.Stream)
				continue
			}
			streams[event.Stream] = true
		}
		if queuedBytes.Add(int64(len(raw))) > 16<<20 {
			queuedBytes.Add(-int64(len(raw)))
			sendError("queue_full", "连接等待数据超过 16 MiB", event.Stream)
			continue
		}
		if h.wsQueueBytes.Add(int64(len(raw))) > 128<<20 {
			h.wsQueueBytes.Add(-int64(len(raw)))
			queuedBytes.Add(-int64(len(raw)))
			sendError("queue_full", "网关 WebSocket 等待数据达到总上限", event.Stream)
			continue
		}
		select {
		case queue <- raw:
		case <-ctx.Done():
			h.wsQueueBytes.Add(-int64(len(raw)))
			return
		default:
			h.wsQueueBytes.Add(-int64(len(raw)))
			queuedBytes.Add(-int64(len(raw)))
			sendError("queue_full", "当前连接等待请求过多，请等待已有响应完成", event.Stream)
		}
	}
}

type websocketWriter struct {
	header   http.Header
	status   int
	buffer   bytes.Buffer
	write    func([]byte) error
	turn     *websocketTurn
	writeErr error
}

func (w *websocketWriter) Header() http.Header { return w.header }
func (w *websocketWriter) WriteHeader(status int) {
	if w.status == 0 {
		w.status = status
	}
}
func (w *websocketWriter) SetWriteDeadline(time.Time) error { return nil }
func (w *websocketWriter) Write(raw []byte) (int, error) {
	if w.writeErr != nil {
		return 0, w.writeErr
	}
	if w.status == 0 {
		w.status = 200
	}
	w.buffer.Write(raw)
	if w.status >= 400 {
		return len(raw), nil
	}
	for {
		data := w.buffer.Bytes()
		at := bytes.Index(data, []byte("\n\n"))
		if at < 0 {
			break
		}
		frame := append([]byte(nil), data[:at]...)
		w.buffer.Next(at + 2)
		for _, line := range bytes.Split(frame, []byte("\n")) {
			if bytes.HasPrefix(line, []byte("data:")) {
				event := bytes.TrimSpace(line[5:])
				if bytes.Equal(event, []byte("[DONE]")) {
					continue
				}
				if err := w.send(event); err != nil {
					w.writeErr = err
					return 0, err
				}
			}
		}
	}
	return len(raw), nil
}
func (w *websocketWriter) send(raw []byte) error {
	var event map[string]json.RawMessage
	if json.Unmarshal(raw, &event) != nil {
		return errors.New("invalid websocket event")
	}
	if w.turn.stream != "" {
		event["stream_id"], _ = json.Marshal(w.turn.stream)
	}
	var kind string
	_ = json.Unmarshal(event["type"], &kind)
	if kind == "error" && w.turn.code() != "" {
		event["error"], _ = json.Marshal(map[string]string{"type": "invalid_request_error", "code": w.turn.code(), "message": "上游会话状态不可用，请发送完整上下文或重新连接"})
	}
	encoded, _ := json.Marshal(event)
	return w.write(encoded)
}
func (w *websocketWriter) Flush() {}
func (w *websocketWriter) finish() {
	if w.status >= 400 {
		var source struct {
			Error struct {
				Message string `json:"message"`
			} `json:"error"`
			RequestID string `json:"request_id"`
		}
		_ = json.Unmarshal(w.buffer.Bytes(), &source)
		code := w.turn.code()
		if code == "" {
			code = "gateway_error"
		}
		if code == "unsupported_transport" {
			source.Error.Message = "此上游账号未开启原生 WebSocket，请在账号配置中启用或使用 HTTP/SSE"
		}
		raw, _ := json.Marshal(map[string]any{"type": "error", "status": w.status, "request_id": source.RequestID, "stream_id": w.turn.stream, "error": map[string]string{"type": "gateway_error", "code": code, "message": source.Error.Message}})
		_ = w.write(raw)
	}
}

// http.Server.Shutdown does not own hijacked WebSockets. Cancel and wait for
// their ordinary settlement path before closing SQLite during process shutdown.
func (h *Handler) registerWebSocket(cancel context.CancelFunc) (uint64, bool) {
	h.wsMu.Lock()
	defer h.wsMu.Unlock()
	if h.wsClosing {
		return 0, false
	}
	if h.wsCancels == nil {
		h.wsCancels = make(map[uint64]context.CancelFunc)
	}
	id := h.wsID.Add(1)
	h.wsCancels[id] = cancel
	h.wsWG.Add(1)
	return id, true
}
func (h *Handler) unregisterWebSocket(id uint64) {
	h.wsMu.Lock()
	delete(h.wsCancels, id)
	h.wsMu.Unlock()
	h.wsWG.Done()
}
func (h *Handler) ShutdownWebSockets(ctx context.Context) error {
	h.wsMu.Lock()
	h.wsClosing = true
	cancels := make([]context.CancelFunc, 0, len(h.wsCancels))
	for _, cancel := range h.wsCancels {
		cancels = append(cancels, cancel)
	}
	h.wsMu.Unlock()
	for _, cancel := range cancels {
		cancel()
	}
	done := make(chan struct{})
	go func() { h.wsWG.Wait(); close(done) }()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
