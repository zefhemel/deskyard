package server

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/coder/websocket"
)

type uiEventWriter interface {
	send(kind, data string)
	comment()
}

// WebSockets avoid the browser's six HTTP/1 connection slots being occupied
// by permanent event feeds. Keep SSE for existing clients and API consumers.
func (s *Server) startUIEvents(w http.ResponseWriter, r *http.Request) (context.Context, uiEventWriter, func()) {
	if !strings.EqualFold(r.Header.Get("Upgrade"), "websocket") {
		writer := startSSE(w)
		if writer == nil {
			return r.Context(), nil, func() {}
		}
		return r.Context(), writer, func() {}
	}
	if !s.terminalOrigin(r) {
		writeError(w, http.StatusForbidden, "cross-origin request refused")
		return r.Context(), nil, func() {}
	}
	ws, err := websocket.Accept(w, r, nil)
	if err != nil {
		return r.Context(), nil, func() {}
	}
	ctx, cancel := context.WithCancel(r.Context())
	// A read loop processes close/pong frames and rejects application input.
	// Own the loop instead of CloseRead: its policy close waits for its own
	// read goroutine in websocket v1.8.15, delaying subscriber cleanup.
	stop := context.AfterFunc(ctx, func() { ws.CloseNow() })
	go func() {
		defer cancel()
		if _, _, err := ws.Reader(ctx); err == nil {
			ws.Close(websocket.StatusPolicyViolation, "unexpected data message")
		}
	}()
	return ctx, &socketEventWriter{ctx: ctx, ws: ws, cancel: cancel}, func() {
		cancel()
		stop()
		ws.CloseNow()
	}
}

type socketEventWriter struct {
	ctx    context.Context
	ws     *websocket.Conn
	cancel context.CancelFunc
}

func (s *socketEventWriter) send(kind, data string) {
	b, err := json.Marshal(struct {
		Type string          `json:"type"`
		Data json.RawMessage `json:"data"`
	}{kind, json.RawMessage(data)})
	if err != nil {
		s.cancel()
		return
	}
	ctx, cancel := context.WithTimeout(s.ctx, 5*time.Second)
	defer cancel()
	if s.ws.Write(ctx, websocket.MessageText, b) != nil {
		s.cancel()
	}
}

func (s *socketEventWriter) comment() {
	ctx, cancel := context.WithTimeout(s.ctx, 5*time.Second)
	defer cancel()
	if s.ws.Ping(ctx) != nil {
		s.cancel()
	}
}
