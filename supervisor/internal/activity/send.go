package activity

import (
	"encoding/json"
	"net"
	"time"
)

// Send puts one event on the inside stream: a UDP datagram to StreamAddr. It
// never blocks for long and never fails the caller; if nothing listens, or
// the event is too big, it is dropped.
func Send(e Event) { sendTo(StreamAddr, e) }

func sendTo(addr string, e Event) {
	b, err := json.Marshal(e)
	if err != nil || len(b) > MaxDatagram {
		return
	}
	c, err := net.Dial("udp", addr)
	if err != nil {
		return
	}
	defer c.Close()
	_ = c.SetWriteDeadline(time.Now().Add(50 * time.Millisecond))
	_, _ = c.Write(b)
}
