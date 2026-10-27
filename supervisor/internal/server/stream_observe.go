package server

import (
	"encoding/binary"
	"io"
	"slices"
	"strconv"
	"strings"
)

// observeFilter sits on an observer's stream connection to Selkies and lets
// through only the client messages a viewer needs to receive the picture:
// keyboard, mouse, clipboard, resizes, settings, uploads and everything else
// that would act on the shared desktop never reach it. Selkies' side (Read)
// passes as it is.
//
// It reads the client's WebSocket framing however the bytes are split. A
// frame the filter might forward is at most 139 bytes (an allowed message is
// short, a control frame's payload at most 125), so it is held until complete
// and then forwarded byte for byte, or dropped; any other frame is skipped as
// it streams past without being held. Selkies' stream has no compression
// extension, so payloads are as framed.
//
// Write is not safe for concurrent use: the proxy has one writer, and
// streamWatch serializes its own START_VIDEO with it.
type observeFilter struct {
	conn io.ReadWriteCloser

	frame [maxHeldFrame]byte // the current frame's header so far, then the whole of a held frame
	n     int                // bytes of it so far
	need  int                // its header's length, then the whole frame's, once known
	head  int                // its header's length, once the frame is held
	hold  bool               // the header is in and the frame is held
	skip  uint64             // payload bytes of a dropped frame still to come
}

const (
	maxObservedText = 125 // longer texts are none an observer may send
	maxHeldFrame    = 14 + 125
)

func newObserveFilter(conn io.ReadWriteCloser) io.ReadWriteCloser {
	return &observeFilter{conn: conn}
}

func (f *observeFilter) Read(p []byte) (int, error) { return f.conn.Read(p) }

func (f *observeFilter) Close() error { return f.conn.Close() }

// Write takes the client's side, going to Selkies. Dropped bytes count as
// written.
func (f *observeFilter) Write(p []byte) (int, error) {
	for i := 0; i < len(p); {
		if f.skip > 0 {
			take := min(f.skip, uint64(len(p)-i))
			f.skip -= take
			i += int(take)
			continue
		}
		f.frame[f.n] = p[i]
		f.n++
		i++
		if !f.hold {
			if f.n == 2 {
				f.need = 2
				switch f.frame[1] & 0x7f {
				case 126:
					f.need += 2
				case 127:
					f.need += 8
				}
				if f.frame[1]&0x80 != 0 {
					f.need += 4
				}
			}
			if f.n < 2 || f.n < f.need {
				continue
			}
			f.header()
		}
		if f.hold && f.n == f.need {
			if f.forward() {
				if _, err := f.conn.Write(f.frame[:f.n]); err != nil {
					f.n, f.hold = 0, false
					return i, err
				}
			}
			f.n, f.hold = 0, false
		}
	}
	return len(p), nil
}

// header decides on a frame whose header is complete: held until its end,
// or skipped.
func (f *observeFilter) header() {
	h := f.frame[:f.n]
	fin, rsv, opcode, masked := h[0]&0x80 != 0, h[0]&0x70, h[0]&0x0f, h[1]&0x80 != 0
	var length uint64
	switch n := h[1] & 0x7f; n {
	case 126:
		length = uint64(binary.BigEndian.Uint16(h[2:]))
	case 127:
		length = binary.BigEndian.Uint64(h[2:])
	default:
		length = uint64(n)
	}
	var held bool
	switch opcode {
	case 0x8, 0x9, 0xA: // close, ping, pong
		held = fin && rsv == 0 && length <= 125
	case wsText:
		// Not a fragment (fragmented messages, continuations included, go
		// whole), not compressed, masked as every browser frame is.
		held = fin && rsv == 0 && masked && length <= maxObservedText
	}
	if !held {
		f.n, f.skip = 0, length
		return
	}
	f.hold, f.head = true, f.n
	f.need = f.n + int(length)
}

// forward decides on a complete held frame.
func (f *observeFilter) forward() bool {
	opcode := f.frame[0] & 0x0f
	if opcode != wsText {
		return true // a control frame
	}
	payload := slices.Clone(f.frame[f.head:f.n]) // unmasked as a copy: the frame goes as it came
	mask := f.frame[f.head-4 : f.head]
	for i := range payload {
		payload[i] ^= mask[i%4]
	}
	return observerMayForward(payload)
}

// observerMayForward reports whether an observer may send Selkies this text
// message. Selkies takes the supervisor's stream as a controller's, so this
// list is all that stands between an observer and the desktop. Without the
// page's SETTINGS the connection never owns the display: Selkies streams it
// the desktop as it is, as it does a shared viewer.
//
// Allowed:
//   - START_VIDEO: starts this connection's picture (for a connection without
//     a display Selkies resets its decoder and sends a keyframe, or starts the
//     capture at the desktop's current size); streamWatch sends it too.
//   - REQUEST_KEYFRAME: the decoder's recovery; a keyframe of the shared
//     stream, rate-limited per connection by Selkies.
//   - LOST_FRAME <id>: the decoder's recovery without a keyframe, likewise.
//   - _gz,1: compression of this connection's own messages.
//
// Dropped, among everything else:
//   - SETTINGS,…: would make the observer the display's owner (its size,
//     encoder and density) or, beside the owner, apply its picks; the picture
//     needs none of it.
//   - kd, ku, kr, kh, co, (keyboard), m, m2, p, vp, sf, sfe, _pointer_echo,
//     SET_NATIVE_CURSOR_RENDERING (mouse, touch and the pointer), js,
//     (gamepads): input.
//   - cw…, cb…, cr, REQUEST_CLIPBOARD: the clipboard, both ways.
//   - r, s: the desktop's size and density.
//   - START_AUDIO, STOP_AUDIO: turn the desktop's sound on or off for
//     everyone; an observer hears it when it is on.
//   - STOP_VIDEO: our client never sends it; a pause is the page's own.
//   - CLIENT_FRAME_ACK, DECODE_PACE: count for the display's owner only.
//   - cmd, and Selkies' commands, _stats: run things or report on them.
//   - every binary message: microphone, webcam, file uploads, gzipped text.
func observerMayForward(text []byte) bool {
	switch s := string(text); s {
	case "START_VIDEO", "REQUEST_KEYFRAME", "_gz,1":
		return true
	default:
		id, ok := strings.CutPrefix(s, "LOST_FRAME ")
		if !ok || id == "" || len(id) > 5 {
			return false
		}
		_, err := strconv.ParseUint(id, 10, 16)
		return err == nil
	}
}
