package server

import (
	"crypto/rand"
	"encoding/binary"
	"io"
	"sync"
	"sync/atomic"
	"time"
)

// streamPictureWait is how long a viewer's stream may go without a picture,
// from the viewer's first message, before the supervisor restarts the
// desktop's stream for it. A joining viewer's first picture comes well
// within it.
const streamPictureWait = 2500 * time.Millisecond

// streamWatch sits on a viewer's stream connection to Selkies (the upgraded
// connection the reverse proxy copies through) and restarts the desktop's
// stream if Selkies has stopped it: when no picture has reached the viewer
// streamPictureWait after its first message, it sends Selkies START_VIDEO on
// the viewer's behalf, once.
//
// Selkies stops a desktop's stream when its only viewer's tab goes to the
// background (STOP_VIDEO); a viewer joining after that would wait for a
// picture until the hidden tab came back. Our client additions never send
// STOP_VIDEO; this is the fallback for when Selkies stops anyway.
//
// It reads the WebSocket framing only: client frames (masked) to know where
// one ends, so START_VIDEO never lands inside one, and Selkies' frames to see
// a picture (a binary message starting 0x03, JPEG, or 0x04, video stripes).
// Selkies' stream has no compression extension, so the bytes are as framed.
type streamWatch struct {
	conn      io.ReadWriteCloser
	onRestart func()

	mu      sync.Mutex // the client side: Write, and the restart
	up      frameScanner
	timer   *time.Timer
	wait    time.Duration
	pending bool // the restart waits for the end of the client's frame
	done    bool // restarted or closed

	// Selkies' side, Read's only: Read never waits for mu, which a Write
	// blocked on a busy Selkies holds.
	down    frameScanner
	picture atomic.Bool // a picture reached the client
}

func newStreamWatch(conn io.ReadWriteCloser, wait time.Duration, onRestart func()) *streamWatch {
	w := &streamWatch{conn: conn, wait: wait, onRestart: onRestart}
	w.up.onHeader = func(opcode byte) {
		// The client's first message starts the wait.
		if w.timer == nil && !w.done && (opcode == wsText || opcode == wsBinary) {
			w.timer = time.AfterFunc(w.wait, w.restart)
		}
	}
	w.down.onFirstByte = func(opcode, b byte) {
		if opcode == wsBinary && (b == 0x03 || b == 0x04) {
			w.picture.Store(true)
		}
	}
	return w
}

// Read is Selkies' side, going to the client.
func (w *streamWatch) Read(p []byte) (int, error) {
	n, err := w.conn.Read(p)
	for rest := p[:n]; len(rest) > 0 && !w.picture.Load(); {
		rest = rest[w.down.next(rest):]
	}
	return n, err
}

// Write is the client's side, going to Selkies.
func (w *streamWatch) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	written := 0
	for written < len(p) {
		k := w.up.next(p[written:])
		n, err := w.conn.Write(p[written : written+k])
		written += n
		if err != nil {
			return written, err
		}
		if w.pending && w.up.atBoundary() && !w.picture.Load() {
			if err := w.sendStart(); err != nil {
				return written, err
			}
		}
	}
	return written, nil
}

// Close closes the connection first: a Write blocked on it holds mu.
func (w *streamWatch) Close() error {
	err := w.conn.Close()
	w.mu.Lock()
	w.stop()
	w.mu.Unlock()
	return err
}

// restart runs when the wait is over without a picture.
func (w *streamWatch) restart() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.done || w.picture.Load() {
		return
	}
	if w.up.atBoundary() {
		_ = w.sendStart() // a failed write ends the connection anyway
	} else {
		w.pending = true
	}
}

// sendStart writes START_VIDEO to Selkies as a client frame; the caller holds mu.
func (w *streamWatch) sendStart() error {
	w.stop()
	if _, err := w.conn.Write(clientTextFrame("START_VIDEO")); err != nil {
		return err
	}
	if w.onRestart != nil {
		w.onRestart()
	}
	return nil
}

// stop ends the watch; the caller holds mu.
func (w *streamWatch) stop() {
	w.done = true
	w.pending = false
	if w.timer != nil {
		w.timer.Stop()
	}
}

const (
	wsText   = 0x1
	wsBinary = 0x2
)

// clientTextFrame is one masked text frame, as a browser sends it.
func clientTextFrame(text string) []byte {
	f := make([]byte, 6, 6+len(text))
	f[0] = 0x80 | wsText // FIN
	f[1] = 0x80 | byte(len(text))
	_, _ = rand.Read(f[2:6])
	for i := 0; i < len(text); i++ {
		f = append(f, text[i]^f[2+i%4])
	}
	return f
}

// frameScanner follows WebSocket frames through a byte stream, however it is
// split: where each frame ends, each frame's opcode, and the first byte of
// its payload (unmasked).
type frameScanner struct {
	onHeader    func(opcode byte)
	onFirstByte func(opcode, b byte)

	hdr       [14]byte
	hn        int // header bytes so far
	need      int // the header's length, once its second byte is in
	inPayload bool
	left      uint64 // payload bytes still to come
	first     bool   // the payload's first byte is still to come
	opcode    byte
	mask      [4]byte
	masked    bool
}

// atBoundary reports whether the stream is between frames.
func (s *frameScanner) atBoundary() bool { return !s.inPayload && s.hn == 0 }

// next consumes p up to the end of the current frame and returns how many
// bytes it took: all of p, or fewer when a frame ends inside it.
func (s *frameScanner) next(p []byte) int {
	i := 0
	for i < len(p) {
		if !s.inPayload {
			s.hdr[s.hn] = p[i]
			s.hn++
			i++
			if s.hn == 2 {
				s.need = 2
				switch s.hdr[1] & 0x7f {
				case 126:
					s.need += 2
				case 127:
					s.need += 8
				}
				if s.hdr[1]&0x80 != 0 {
					s.need += 4
				}
			}
			if s.hn < 2 || s.hn < s.need {
				continue
			}
			s.startPayload()
			if s.left == 0 {
				s.inPayload = false
				return i
			}
			continue
		}
		if s.first {
			s.first = false
			if s.onFirstByte != nil {
				b := p[i]
				if s.masked {
					b ^= s.mask[0]
				}
				s.onFirstByte(s.opcode, b)
			}
		}
		take := uint64(len(p) - i)
		if take > s.left {
			take = s.left
		}
		i += int(take)
		s.left -= take
		if s.left == 0 {
			s.inPayload = false
			return i
		}
	}
	return i
}

// startPayload reads the complete header.
func (s *frameScanner) startPayload() {
	h := s.hdr[:s.need]
	s.opcode = h[0] & 0x0f
	s.masked = h[1]&0x80 != 0
	rest := h[2:]
	switch n := h[1] & 0x7f; n {
	case 126:
		s.left = uint64(binary.BigEndian.Uint16(rest))
		rest = rest[2:]
	case 127:
		s.left = binary.BigEndian.Uint64(rest)
		rest = rest[8:]
	default:
		s.left = uint64(n)
	}
	if s.masked {
		copy(s.mask[:], rest)
	}
	s.hn = 0
	s.inPayload = true
	s.first = true
	if s.onHeader != nil {
		s.onHeader(s.opcode)
	}
}
