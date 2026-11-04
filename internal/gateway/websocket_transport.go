package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"fiftyapi/internal/accounts"
	ws "github.com/coder/websocket"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

type websocketStateKey struct{}
type wsCachedResponse struct{ model string }

// One downstream socket owns one authenticated native upstream connection. Turns
// use the regular HTTP/SSE scheduling/ledger machinery via a transport adapter;
// no prompt history is reconstructed or persisted to emulate previous IDs.
type websocketState struct {
	mu        sync.Mutex
	conn      *ws.Conn
	owner     int64
	cache     map[string]wsCachedResponse
	order     []string
	lastModel string
	base      *http.Client
}

func (s *websocketState) Owner() int64 { s.mu.Lock(); defer s.mu.Unlock(); return s.owner }
func (s *websocketState) close() {
	s.mu.Lock()
	conn := s.conn
	base := s.base
	s.conn = nil
	s.base = nil
	s.owner = 0
	s.cache = make(map[string]wsCachedResponse)
	s.order = nil
	s.lastModel = ""
	s.mu.Unlock()
	if conn != nil {
		conn.CloseNow()
	}
	if base != nil {
		base.CloseIdleConnections()
	}
}
func (s *websocketState) canSwitch() bool { s.mu.Lock(); defer s.mu.Unlock(); return len(s.cache) == 0 }
func (s *websocketState) previous(id string) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	entry, ok := s.cache[id]
	return entry.model, ok && s.conn != nil
}
func (s *websocketState) remember(id, model string) {
	if id == "" || len(id) > 256 {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cache == nil {
		s.cache = map[string]wsCachedResponse{}
	}
	if _, ok := s.cache[id]; !ok {
		s.order = append(s.order, id)
	}
	s.cache[id] = wsCachedResponse{model: model}
	s.lastModel = model
	for len(s.order) > 64 {
		delete(s.cache, s.order[0])
		s.order = s.order[1:]
	}
}

type websocketTurn struct {
	state                   *websocketState
	ctx                     context.Context
	model, stream, previous string
	mu                      sync.Mutex
	protocolCode            string
}

func (t *websocketTurn) setCode(code string) { t.mu.Lock(); t.protocolCode = code; t.mu.Unlock() }
func (t *websocketTurn) code() string        { t.mu.Lock(); defer t.mu.Unlock(); return t.protocolCode }
func (t *websocketTurn) client(h *Handler, c accounts.Credential) *http.Client {
	base := h.client(c.Config)
	return &http.Client{Timeout: base.Timeout, Transport: &websocketUpstreamTransport{turn: t, credential: c, base: base}, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
}

type websocketUpstreamTransport struct {
	turn       *websocketTurn
	credential accounts.Credential
	base       *http.Client
}

func (t *websocketUpstreamTransport) CloseIdleConnections() {} // the socket owns transport lifetime
func (t *websocketUpstreamTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.Method != http.MethodPost || !strings.HasSuffix(req.URL.Path, "/responses") {
		t.base.CloseIdleConnections()
		return nil, errors.New("unsupported websocket upstream path")
	}
	if t.credential.Config.Provider == "gateway" && (t.credential.Config.Gateway == nil || !t.credential.Config.Gateway.WebSocket) {
		t.turn.setCode("unsupported_transport")
		t.base.CloseIdleConnections()
		return websocketRejection(req, 400, "unsupported_transport"), nil
	}
	state := t.turn.state
	state.mu.Lock()
	conn, owner := state.conn, state.owner
	state.mu.Unlock()
	if conn != nil && owner != t.credential.AccountID {
		t.base.CloseIdleConnections()
		return nil, errors.New("websocket upstream owner changed")
	}
	if conn == nil && t.turn.previous != "" {
		t.turn.setCode("previous_response_not_found")
		t.base.CloseIdleConnections()
		return websocketRejection(req, 400, "previous_response_not_found"), nil
	}
	var handshake http.Header
	if conn == nil {
		u := *req.URL
		switch u.Scheme {
		case "https":
			u.Scheme = "wss"
		case "http":
			if t.credential.Config.Provider != "gateway" || t.credential.Config.Gateway == nil || !t.credential.Config.Gateway.AllowHTTP {
				t.base.CloseIdleConnections()
				return nil, errors.New("insecure websocket upstream disabled")
			}
			u.Scheme = "ws"
		default:
			t.base.CloseIdleConnections()
			return nil, errors.New("invalid websocket scheme")
		}
		headers := req.Header.Clone()
		for _, name := range []string{"Connection", "Upgrade", "Content-Length", "Content-Type", "Accept", "Sec-Websocket-Key", "Sec-Websocket-Version", "Sec-Websocket-Protocol"} {
			headers.Del(name)
		}
		if t.credential.Config.Provider == "openai" && !strings.Contains(headers.Get("OpenAI-Beta"), "responses_websockets=") {
			value := headers.Get("OpenAI-Beta")
			if value != "" {
				value += ","
			}
			headers.Set("OpenAI-Beta", value+"responses_websockets=2026-02-06")
		}
		ctx, cancel := context.WithTimeout(req.Context(), 20*time.Second)
		var response *http.Response
		var err error
		conn, response, err = ws.Dial(ctx, u.String(), &ws.DialOptions{HTTPClient: t.base, HTTPHeader: headers})
		cancel()
		if err != nil {
			t.base.CloseIdleConnections()
			if response != nil {
				if response.Body == nil {
					response.Body = io.NopCloser(strings.NewReader(""))
				}
				return response, nil
			}
			return nil, errors.New("websocket upstream connection failed")
		}
		conn.SetReadLimit(2 << 20)
		if response != nil {
			handshake = response.Header.Clone()
		}
		state.mu.Lock()
		state.conn = conn
		state.owner = t.credential.AccountID
		state.base = t.base
		state.mu.Unlock()
	} else {
		t.base.CloseIdleConnections()
	}
	raw, err := io.ReadAll(io.LimitReader(req.Body, 4<<20))
	if err != nil {
		return nil, errors.New("websocket request read failed")
	}
	var payload map[string]json.RawMessage
	if json.Unmarshal(raw, &payload) != nil {
		return nil, errors.New("websocket request invalid")
	}
	payload["type"] = json.RawMessage(`"response.create"`)
	delete(payload, "stream")
	delete(payload, "background")
	encoded, _ := json.Marshal(payload)
	ctx, cancel := context.WithCancel(req.Context())
	if err = conn.Write(ctx, ws.MessageText, encoded); err != nil {
		cancel()
		state.close()
		return nil, errors.New("websocket request write failed")
	}
	reader, writer := io.Pipe()
	body := &websocketResponseBody{PipeReader: reader, cancel: cancel, state: state}
	go func() {
		defer writer.Close()
		defer cancel()
		for {
			kind, message, e := conn.Read(ctx)
			if e != nil {
				state.close()
				writer.CloseWithError(errors.New("upstream websocket disconnected"))
				return
			}
			if kind != ws.MessageText {
				state.close()
				writer.CloseWithError(errors.New("unexpected binary websocket frame"))
				return
			}
			var event struct {
				Type  string `json:"type"`
				Error struct {
					Code string `json:"code"`
				} `json:"error"`
				Response struct {
					ID    string `json:"id"`
					Error struct {
						Code string `json:"code"`
					} `json:"error"`
				} `json:"response"`
			}
			if json.Unmarshal(message, &event) != nil || event.Type == "" {
				state.close()
				writer.CloseWithError(errors.New("upstream websocket event invalid"))
				return
			}
			code := event.Error.Code
			if code == "" {
				code = event.Response.Error.Code
			}
			switch code {
			case "previous_response_not_found", "invalid_stream_id", "websocket_stream_limit_reached", "websocket_connection_limit_reached":
				t.turn.setCode(code)
			}
			terminal := event.Type == "response.completed" || event.Type == "response.failed" || event.Type == "response.incomplete" || event.Type == "error"
			if event.Type == "response.completed" {
				state.remember(event.Response.ID, t.turn.model)
			}
			if terminal {
				body.finished.Store(true)
			}
			if _, e = writer.Write(append(append([]byte("data: "), message...), []byte("\n\n")...)); e != nil {
				if !terminal {
					state.close()
				}
				return
			}
			if terminal {
				return
			}
		}
	}()
	headers := handshake
	if headers == nil {
		headers = make(http.Header)
	}
	headers.Set("Content-Type", "text/event-stream")
	return &http.Response{StatusCode: 200, Header: headers, Body: body, Request: req}, nil
}

type websocketResponseBody struct {
	*io.PipeReader
	finished atomic.Bool
	cancel   context.CancelFunc
	state    *websocketState
}

func (b *websocketResponseBody) Close() error {
	err := b.PipeReader.Close()
	b.cancel()
	if !b.finished.Load() {
		b.state.close()
	}
	return err
}
func websocketRejection(req *http.Request, status int, code string) *http.Response {
	body, _ := json.Marshal(map[string]any{"error": map[string]string{"code": code}})
	return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(string(body))), Request: req}
}
