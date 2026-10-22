package activity

import (
	"slices"
	"strings"
	"time"
	"unicode/utf8"
)

// Event is one line of the inside stream: what a program in the desktop did.
// The stream is untrusted input; Sanitize is the way in.
type Event struct {
	Seq    uint64        `json:"seq"`
	T      time.Time     `json:"t"`
	Agent  string        `json:"agent"` // program name, inside only
	Kind   string        `json:"kind"`  // "screen", "cmd-start", "cmd-end"
	Screen *ScreenAction `json:"screen,omitempty"`
	ID     string        `json:"id,omitempty"`  // pairs cmd-start and cmd-end
	Cmd    string        `json:"cmd,omitempty"` // display form
	Cwd    string        `json:"cwd,omitempty"`
	Exit   *int          `json:"exit,omitempty"`
}

// Field limits for events from inside a desktop.
const (
	maxAgent = 40
	maxID    = 32
	maxCwd   = 200
	maxKeys  = 8
	maxKey   = 32
	maxPos   = 65535
	maxN     = 100
	maxChars = 1_000_000
	maxIn    = 4096 // longest input looked at before masking
)

// Sanitize makes an event from inside a desktop safe to keep and show: every
// field is capped, control characters are removed, the command is masked
// again, and typed text can't ride along. It returns false for an event that
// makes no sense. It never changes or retains its argument.
func Sanitize(e Event) (Event, bool) {
	out := Event{Seq: e.Seq, T: e.T, Agent: clean(e.Agent, maxAgent), Kind: e.Kind}
	if out.Agent == "" {
		return Event{}, false
	}
	switch e.Kind {
	case "screen":
		s, ok := sanitizeScreen(e.Screen)
		if !ok {
			return Event{}, false
		}
		out.Screen = &s
		return out, true
	case "cmd-start", "cmd-end":
		out.ID = clean(e.ID, maxID)
		if e.Kind == "cmd-start" && out.ID == "" {
			return Event{}, false
		}
		out.Cmd = cutCmd(strings.TrimSpace(flatten(MaskText(plainText(cutRunes(e.Cmd, maxIn))))))
		out.Cwd = clean(e.Cwd, maxCwd)
		if e.Exit != nil {
			x := *e.Exit
			out.Exit = &x
		}
		return out, true
	}
	return Event{}, false
}

// posLen is how many numbers each action's Pos has; absent means none.
var posLen = map[string]int{"drag": 4, "click": 2, "double-click": 2, "triple-click": 2, "move": 2, "scroll": 2}

func sanitizeScreen(s *ScreenAction) (ScreenAction, bool) {
	if s == nil || !slices.Contains(ScreenCommands, s.Action) {
		return ScreenAction{}, false
	}
	if len(s.Pos) != posLen[s.Action] { // later code indexes Pos by action
		return ScreenAction{}, false
	}
	out := ScreenAction{Action: s.Action}
	if s.Action == "type" {
		out.Chars = clamp(s.Chars, maxChars) // the count, never the text
		return out, true
	}
	for _, p := range s.Pos {
		out.Pos = append(out.Pos, clamp(p, maxPos))
	}
	if slices.Contains(buttons, s.Button) {
		out.Button = s.Button
	}
	if slices.Contains(scrollTo, s.Dir) {
		out.Dir = s.Dir
	}
	out.N = clamp(s.N, maxN)
	for _, k := range s.Keys[:min(len(s.Keys), maxKeys)] {
		if k = clean(k, maxKey); k != "" {
			out.Keys = append(out.Keys, k)
		}
	}
	return out, true
}

func clamp(v, hi int) int { return max(0, min(v, hi)) }

// clean removes invalid UTF-8 and control characters, trims, and keeps at
// most n runes.
func clean(s string, n int) string {
	s = cutRunes(s, n*4) // bound the work on hostile input; a bad run rarely eats more
	s = flatten(plainText(s))
	return strings.TrimSpace(cutRunes(s, n))
}

// cutCmd keeps a command at 120 runes; a display form already cut with "…"
// (121 runes) passes through unchanged.
func cutCmd(s string) string {
	if utf8.RuneCountInString(s) == maxDisplay+1 && strings.HasSuffix(s, "…") {
		return s
	}
	return strings.TrimSpace(cutRunes(s, maxDisplay))
}
