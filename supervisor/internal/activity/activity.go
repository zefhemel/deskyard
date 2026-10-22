// Package activity is the shared vocabulary of agent activity: who the agents
// are, what a screen action looks like, the format of the stream that
// activity inside a desktop is written to, and how secrets are masked before
// anything is recorded or shown.
package activity

import "unicode/utf16"

// StreamAddr is where a desktop's activity-serve listens: UDP in, HTTP out.
const StreamAddr = "127.0.0.1:6092"

// MaxDatagram is the largest event datagram sent to the stream.
const MaxDatagram = 4096

// Ref names a desktop: its display name and its id.
type Ref struct{ Name, ID string }

// Agent is something that acts on a desktop: an outside agent (a signed-in
// account's agent token) or a program running inside the desktop.
type Agent struct {
	ID, Name, Owner string
	Inside          bool
}

// InsideAgent is the agent for a program running inside a desktop: one per
// program name, with no owner.
func InsideAgent(program string) Agent {
	return Agent{ID: "inside:" + program, Name: program, Inside: true}
}

// The avatar palette; the same six colours as web/src/components/avatar.tsx.
var colors = [...]string{"#2457c5", "#1e8a5a", "#9a4fb5", "#b7791f", "#c2410c", "#0e7490"}

// Color is a fixed colour for an agent id, the same hash as the web's
// avatarColor: h = h*31 + code unit, modulo 2^32.
func Color(id string) string {
	var h uint32
	for _, r := range id {
		if r > 0xFFFF {
			r, _ = utf16.EncodeRune(r) // JS charCodeAt(0) sees the high surrogate
		}
		h = h*31 + uint32(r)
	}
	return colors[h%uint32(len(colors))]
}
