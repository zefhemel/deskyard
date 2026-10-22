package activity

import (
	"net"
	"strings"
	"testing"
	"time"
)

func TestSendNeverBlocks(t *testing.T) {
	// A port nobody listens on: grab one and close it.
	l, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	dead := l.LocalAddr().String()
	l.Close()
	start := time.Now()
	sendTo(dead, Event{Agent: "a terminal", Kind: "cmd-start", ID: "x", Cmd: "ls"})
	if d := time.Since(start); d > 100*time.Millisecond {
		t.Errorf("send took %v", d)
	}

	l, err = net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	addr := l.LocalAddr().String()
	buf := make([]byte, 65536)
	big := Event{Agent: "a terminal", Kind: "cmd-start", ID: "x", Cmd: strings.Repeat("a", MaxDatagram)}
	sendTo(addr, big)
	l.SetReadDeadline(time.Now().Add(200 * time.Millisecond))
	if n, _, err := l.ReadFrom(buf); err == nil {
		t.Errorf("an oversize event was sent (%d bytes)", n)
	}
	sendTo(addr, Event{Agent: "a terminal", Kind: "cmd-start", ID: "x", Cmd: "ls"})
	l.SetReadDeadline(time.Now().Add(time.Second))
	n, _, err := l.ReadFrom(buf)
	if err != nil || !strings.Contains(string(buf[:n]), `"cmd":"ls"`) {
		t.Errorf("normal event not received: %v %q", err, buf[:n])
	}
}
