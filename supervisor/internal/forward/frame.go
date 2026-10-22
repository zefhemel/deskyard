// Package forward multiplexes TCP port mappings over one byte stream: a
// WebSocket through the supervisor (one frame per message) or stdio (frames
// with a u32 length prefix). See docs/superpowers/specs/2026-10-22-audio-and-ports-design.md.
package forward

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"

	"github.com/coder/websocket"
)

// Frame types. A frame is: type u8 | stream u32 BE | payload.
const (
	TypeListen    byte = 1 // payload: port u16. Ask the desktop side to listen on 127.0.0.1:port; stream is the listener id.
	TypeListening byte = 2 // empty: the listen worked.
	TypeOpen      byte = 3 // payload: port u16. A new connection; the receiver dials 127.0.0.1:port.
	TypeData      byte = 4
	TypeClose     byte = 5 // half-close of the sender's direction (for a listener id: stop listening).
	TypeReset     byte = 6 // payload: UTF-8 error. Listener id: listen failed. Stream: dial failed or aborted.
	TypeWindow    byte = 7 // payload: n u32. Grants n more bytes.
)

const (
	headerLen     = 5
	maxData       = 32 << 10            // largest Data payload
	maxFrame      = headerLen + maxData // largest frame accepted
	initialWindow = 256 << 10           // per stream, each direction

	// MaxFrame is the largest frame (header and payload, without a length
	// prefix) either side accepts; a relay can drop anything longer.
	MaxFrame = maxFrame
)

func encodeFrame(typ byte, stream uint32, payload []byte) []byte {
	b := make([]byte, headerLen+len(payload))
	b[0] = typ
	binary.BigEndian.PutUint32(b[1:], stream)
	copy(b[headerLen:], payload)
	return b
}

func portPayload(port int) []byte {
	var p [2]byte
	binary.BigEndian.PutUint16(p[:], uint16(port))
	return p[:]
}

// Peek decodes one frame (without any length prefix) so the supervisor can
// observe Listen and Listening without running a Mux. The payload aliases b.
func Peek(b []byte) (typ byte, stream uint32, payload []byte, err error) {
	if len(b) < headerLen {
		return 0, 0, nil, errors.New("forward: short frame")
	}
	return b[0], binary.BigEndian.Uint32(b[1:]), b[headerLen:], nil
}

// PortOf returns the port in a Listen or Open payload.
func PortOf(payload []byte) (int, error) {
	if len(payload) < 2 {
		return 0, errors.New("forward: short port payload")
	}
	return int(binary.BigEndian.Uint16(payload)), nil
}

// readFrame reads one frame from rw. buf must hold maxFrame bytes.
func readFrame(rw io.Reader, framed bool, buf []byte) ([]byte, error) {
	if framed {
		n, err := rw.Read(buf)
		if err != nil {
			return nil, err
		}
		return buf[:n], nil
	}
	var l [4]byte
	if _, err := io.ReadFull(rw, l[:]); err != nil {
		return nil, err
	}
	n := binary.BigEndian.Uint32(l[:])
	if n < headerLen || n > maxFrame {
		return nil, fmt.Errorf("forward: bad frame length %d", n)
	}
	if _, err := io.ReadFull(rw, buf[:n]); err != nil {
		return nil, err
	}
	return buf[:n], nil
}

func writeFrame(w io.Writer, framed bool, frame []byte) error {
	if framed {
		_, err := w.Write(frame)
		return err
	}
	b := make([]byte, 4+len(frame))
	binary.BigEndian.PutUint32(b, uint32(len(frame)))
	copy(b[4:], frame)
	_, err := w.Write(b)
	return err
}

// WSMessages adapts a WebSocket to the io.ReadWriteCloser a framed Mux
// wants: each Read returns exactly one binary message, each Write sends one.
type WSMessages struct {
	ctx context.Context
	c   *websocket.Conn
}

// NewWSMessages wraps c; ctx bounds every read and write.
func NewWSMessages(ctx context.Context, c *websocket.Conn) *WSMessages {
	c.SetReadLimit(maxFrame)
	return &WSMessages{ctx: ctx, c: c}
}

func (w *WSMessages) Read(p []byte) (int, error) {
	_, data, err := w.c.Read(w.ctx)
	if err != nil {
		if websocket.CloseStatus(err) == websocket.StatusNormalClosure {
			return 0, io.EOF
		}
		return 0, err
	}
	if len(data) > len(p) {
		return 0, io.ErrShortBuffer
	}
	return copy(p, data), nil
}

func (w *WSMessages) Write(p []byte) (int, error) {
	if err := w.c.Write(w.ctx, websocket.MessageBinary, p); err != nil {
		return 0, err
	}
	return len(p), nil
}

func (w *WSMessages) Close() error { return w.c.Close(websocket.StatusNormalClosure, "") }
