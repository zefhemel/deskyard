package server

import (
	"bytes"
	"encoding/binary"
	"io"
	"sync"
	"testing"
	"time"
)

// wsFrame builds one WebSocket frame: masked as a browser sends them, or not
// as a server does.
func wsFrame(opcode byte, payload []byte, masked bool) []byte {
	var b bytes.Buffer
	b.WriteByte(0x80 | opcode)
	m := byte(0)
	if masked {
		m = 0x80
	}
	switch n := len(payload); {
	case n < 126:
		b.WriteByte(m | byte(n))
	case n < 1<<16:
		b.WriteByte(m | 126)
		_ = binary.Write(&b, binary.BigEndian, uint16(n))
	default:
		b.WriteByte(m | 127)
		_ = binary.Write(&b, binary.BigEndian, uint64(n))
	}
	if !masked {
		b.Write(payload)
		return b.Bytes()
	}
	key := [4]byte{0x11, 0x22, 0x33, 0x44}
	b.Write(key[:])
	for i, c := range payload {
		b.WriteByte(c ^ key[i%4])
	}
	return b.Bytes()
}

// unmaskedText reads the text frames a client wrote (masked) back out.
func clientTexts(t *testing.T, data []byte) []string {
	t.Helper()
	var out []string
	for len(data) > 0 {
		opcode := data[0] & 0x0f
		n := int(data[1] & 0x7f)
		i := 2
		switch n {
		case 126:
			n = int(binary.BigEndian.Uint16(data[2:]))
			i = 4
		case 127:
			n = int(binary.BigEndian.Uint64(data[2:]))
			i = 10
		}
		if data[1]&0x80 == 0 {
			t.Fatalf("client frame not masked: % x", data[:i])
		}
		key := data[i : i+4]
		i += 4
		p := make([]byte, n)
		for j := range p {
			p[j] = data[i+j] ^ key[j%4]
		}
		if opcode == 1 {
			out = append(out, string(p))
		} else {
			out = append(out, "<binary>")
		}
		data = data[i+n:]
	}
	return out
}

// fakeUpstream is Selkies' end of a stream: what the client writes, and what
// Selkies sends it.
type fakeUpstream struct {
	mu      sync.Mutex
	written bytes.Buffer
	toRead  chan []byte
}

func newFakeUpstream() *fakeUpstream { return &fakeUpstream{toRead: make(chan []byte, 16)} }

func (f *fakeUpstream) Write(p []byte) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.written.Write(p)
}

func (f *fakeUpstream) Read(p []byte) (int, error) {
	b, ok := <-f.toRead
	if !ok {
		return 0, io.EOF
	}
	return copy(p, b), nil
}

func (f *fakeUpstream) Close() error { return nil }

func (f *fakeUpstream) sent() []byte {
	f.mu.Lock()
	defer f.mu.Unlock()
	return bytes.Clone(f.written.Bytes())
}

func watched(up *fakeUpstream, wait time.Duration) (*streamWatch, chan struct{}) {
	restarted := make(chan struct{}, 1)
	w := newStreamWatch(up, wait, func() { restarted <- struct{}{} })
	return w, restarted
}

func TestStreamWatchRestartsAStreamWithoutPicture(t *testing.T) {
	up := newFakeUpstream()
	w, restarted := watched(up, 50*time.Millisecond)
	defer w.Close()
	settings := wsFrame(1, []byte(`SETTINGS,{"displayId":"primary"}`), true)
	if _, err := w.Write(settings); err != nil {
		t.Fatal(err)
	}
	select {
	case <-restarted:
	case <-time.After(2 * time.Second):
		t.Fatal("no restart")
	}
	got := clientTexts(t, up.sent())
	want := []string{`SETTINGS,{"displayId":"primary"}`, "START_VIDEO"}
	if len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("upstream got %q, want %q", got, want)
	}
}

func TestStreamWatchLeavesAStreamWithPicture(t *testing.T) {
	up := newFakeUpstream()
	w, restarted := watched(up, 100*time.Millisecond)
	defer w.Close()
	if _, err := w.Write(wsFrame(1, []byte("SETTINGS,{}"), true)); err != nil {
		t.Fatal(err)
	}
	// Sound, a text message, then a video stripe split across reads.
	video := wsFrame(2, append([]byte{0x04}, make([]byte, 300)...), false)
	for _, b := range [][]byte{wsFrame(2, []byte{0x01, 0, 9, 9}, false), wsFrame(1, []byte("PIPELINE_RESETTING primary"), false), video[:1], video[1:3], video[3:]} {
		up.toRead <- b
		if _, err := w.Read(make([]byte, 4096)); err != nil {
			t.Fatal(err)
		}
	}
	select {
	case <-restarted:
		t.Fatal("restarted a stream that has its picture")
	case <-time.After(300 * time.Millisecond):
	}
	if got := clientTexts(t, up.sent()); len(got) != 1 {
		t.Fatalf("upstream got %q", got)
	}
}

func TestStreamWatchSoundIsNoPicture(t *testing.T) {
	up := newFakeUpstream()
	w, restarted := watched(up, 50*time.Millisecond)
	defer w.Close()
	up.toRead <- wsFrame(2, []byte{0x01, 0, 1, 2}, false)
	if _, err := w.Read(make([]byte, 64)); err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write(wsFrame(1, []byte("SETTINGS,{}"), true)); err != nil {
		t.Fatal(err)
	}
	select {
	case <-restarted:
	case <-time.After(2 * time.Second):
		t.Fatal("no restart")
	}
}

func TestStreamWatchWaitsForTheClientToSpeak(t *testing.T) {
	// The wait starts with the client's first message: before it, Selkies
	// has nothing to stream to it.
	up := newFakeUpstream()
	w, restarted := watched(up, 30*time.Millisecond)
	defer w.Close()
	select {
	case <-restarted:
		t.Fatal("restarted before the client spoke")
	case <-time.After(150 * time.Millisecond):
	}
	if len(up.sent()) != 0 {
		t.Fatalf("upstream got % x", up.sent())
	}
}

func TestStreamWatchNeverSplitsAClientFrame(t *testing.T) {
	// The restart waits for the end of the frame the client is half-way
	// through writing.
	up := newFakeUpstream()
	w, restarted := watched(up, 30*time.Millisecond)
	defer w.Close()
	first := wsFrame(1, []byte("SETTINGS,{}"), true)
	big := wsFrame(2, bytes.Repeat([]byte{0x02}, 70000), true) // mic sound, 64-bit length
	if _, err := w.Write(append(bytes.Clone(first), big[:100]...)); err != nil {
		t.Fatal(err)
	}
	time.Sleep(150 * time.Millisecond)
	select {
	case <-restarted:
		t.Fatal("restarted mid-frame")
	default:
	}
	if _, err := w.Write(append(bytes.Clone(big[100:]), wsFrame(1, []byte("cr"), true)...)); err != nil {
		t.Fatal(err)
	}
	select {
	case <-restarted:
	case <-time.After(2 * time.Second):
		t.Fatal("no restart")
	}
	got := clientTexts(t, up.sent())
	want := []string{"SETTINGS,{}", "<binary>", "START_VIDEO", "cr"}
	if len(got) != len(want) {
		t.Fatalf("upstream got %q, want %q", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("upstream got %q, want %q", got, want)
		}
	}
}

func TestStreamWatchClosedNeverRestarts(t *testing.T) {
	up := newFakeUpstream()
	w, restarted := watched(up, 30*time.Millisecond)
	if _, err := w.Write(wsFrame(1, []byte("SETTINGS,{}"), true)); err != nil {
		t.Fatal(err)
	}
	w.Close()
	select {
	case <-restarted:
		t.Fatal("restarted a closed stream")
	case <-time.After(150 * time.Millisecond):
	}
}
