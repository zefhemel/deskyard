package activity

import (
	"encoding/json"
	"strings"
	"testing"
	"unicode/utf8"
)

func runes(n int) string { return strings.Repeat("é", n) }

func TestSanitize(t *testing.T) {
	good := func(e Event) Event {
		t.Helper()
		out, ok := Sanitize(e)
		if !ok {
			t.Fatalf("dropped %+v", e)
		}
		return out
	}
	e := good(Event{Agent: runes(100), Kind: "cmd-start", ID: runes(50), Cmd: "deploy --token abc", Cwd: runes(300)})
	if utf8.RuneCountInString(e.Agent) != 40 || utf8.RuneCountInString(e.ID) != 32 || utf8.RuneCountInString(e.Cwd) != 200 {
		t.Errorf("caps: %d %d %d", utf8.RuneCountInString(e.Agent), utf8.RuneCountInString(e.ID), utf8.RuneCountInString(e.Cwd))
	}
	if e.Cmd != "deploy --token •••" {
		t.Errorf("cmd = %q", e.Cmd)
	}
	e = good(Event{Agent: "a", Kind: "cmd-start", ID: "1", Cmd: strings.Repeat("x", 500)})
	if utf8.RuneCountInString(e.Cmd) != 120 {
		t.Errorf("cmd cap = %d", utf8.RuneCountInString(e.Cmd))
	}
	// A display form already cut with an ellipsis passes through.
	disp := DisplayCommand([]string{"echo", strings.Repeat("a b ", 100)})
	if e = good(Event{Agent: "a", Kind: "cmd-start", ID: "1", Cmd: disp}); e.Cmd != disp {
		t.Errorf("display form changed: %q", e.Cmd)
	}
	// A secret straddling the cut can't leak: masking happens before cutting.
	e = good(Event{Agent: "a", Kind: "cmd-start", ID: "1", Cmd: strings.Repeat("y", 100) + " --token " + strings.Repeat("s", 50)})
	if strings.Contains(e.Cmd, "ss") {
		t.Errorf("leak: %q", e.Cmd)
	}
	exit := 3
	e = good(Event{Agent: "a", Kind: "cmd-end", Exit: &exit})
	if e.Exit == nil || *e.Exit != 3 {
		t.Errorf("exit lost")
	}

	drop := []Event{
		{Agent: "a", Kind: "nope"},
		{Agent: "a", Kind: "screen"},
		{Agent: "a", Kind: "screen", Screen: &ScreenAction{Action: "wait"}},
		{Agent: "a", Kind: "screen", Screen: &ScreenAction{Action: "click", Pos: []int{1}}},
		{Agent: "a", Kind: "screen", Screen: &ScreenAction{Action: "click", Pos: []int{1, 2, 3}}},
		{Agent: "a", Kind: "screen", Screen: &ScreenAction{Action: "click", Pos: []int{1, 2, 3, 4, 5}}},
		{Agent: "a", Kind: "cmd-start"},
		{Agent: "a", Kind: "screen", Screen: &ScreenAction{Action: "click"}},
		{Agent: "a", Kind: "screen", Screen: &ScreenAction{Action: "click", Pos: []int{1, 2, 3, 4}}},
		{Agent: "a", Kind: "screen", Screen: &ScreenAction{Action: "drag", Pos: []int{1, 2}}},
		{Agent: "a", Kind: "screen", Screen: &ScreenAction{Action: "scroll"}},
		{Agent: "a", Kind: "screen", Screen: &ScreenAction{Action: "move", Pos: []int{1, 2, 3, 4}}},
		{Agent: "a", Kind: "screen", Screen: &ScreenAction{Action: "key", Pos: []int{1, 2}}},
		{Agent: "a", Kind: "screen", Screen: &ScreenAction{Action: "type", Pos: []int{1, 2}}},
		{Agent: "a", Kind: "screen", Screen: &ScreenAction{Action: "screenshot", Pos: []int{1, 2}}},
		{Agent: "", Kind: "cmd-end"},
		{Agent: " \n\t", Kind: "cmd-end"},
	}
	for _, d := range drop {
		if out, ok := Sanitize(d); ok {
			t.Errorf("kept %+v -> %+v", d, out)
		}
	}

	keys := make([]string, 20)
	for i := range keys {
		keys[i] = runes(50)
	}
	e = good(Event{Agent: "a", Kind: "screen", Screen: &ScreenAction{Action: "key", Keys: keys}})
	if len(e.Screen.Keys) != 8 || utf8.RuneCountInString(e.Screen.Keys[0]) != 32 {
		t.Errorf("keys: %d, %d", len(e.Screen.Keys), utf8.RuneCountInString(e.Screen.Keys[0]))
	}
	e = good(Event{Agent: "a", Kind: "screen", Screen: &ScreenAction{Action: "scroll", Pos: []int{-5, 1 << 40}, N: 5000, Dir: "down", Chars: 7}})
	s := e.Screen
	if s.Pos[0] != 0 || s.Pos[1] != 65535 || s.N != 100 || s.Chars != 0 {
		t.Errorf("clamp: %+v", s)
	}
	e = good(Event{Agent: "a", Kind: "screen", Screen: &ScreenAction{Action: "type", Chars: 1 << 40, Keys: []string{"secret"}, Button: "left", N: 3}})
	if s := e.Screen; s.Chars != 1_000_000 || s.Pos != nil || s.Keys != nil || s.Button != "" || s.N != 0 {
		t.Errorf("type: %+v", s)
	}
	e = good(Event{Agent: "a", Kind: "screen", Screen: &ScreenAction{Action: "type", Chars: -4}})
	if e.Screen.Chars != 0 {
		t.Errorf("neg chars: %+v", e.Screen)
	}
	// Sanitize doesn't alias or mutate the caller's data.
	in := Event{Agent: "a", Kind: "screen", Screen: &ScreenAction{Action: "click", Pos: []int{-1, 2}}}
	out := good(in)
	if in.Screen.Pos[0] != -1 || out.Screen == in.Screen {
		t.Errorf("aliased input")
	}
	// Every action's Pos has its own length.
	for _, a := range ScreenCommands {
		n := posLen[a]
		good(Event{Agent: "a", Kind: "screen", Screen: &ScreenAction{Action: a, Pos: make([]int, n)}})
	}
	// Format characters (bidi overrides, zero-width) are dropped.
	e = good(Event{Agent: "a\u202eb\u200b", Kind: "cmd-end", Cwd: "/x\u202ey", Cmd: "ls \u202e"})
	if e.Agent != "ab" || e.Cwd != "/xy" || e.Cmd != "ls" {
		t.Errorf("format chars: %q %q %q", e.Agent, e.Cwd, e.Cmd)
	}
	e = good(Event{Agent: "a", Kind: "cmd-end", Cmd: "x dyt_ab\u200bcd"})
	if strings.Contains(e.Cmd, "dyt_") || strings.Contains(e.Cmd, "cd") {
		t.Errorf("zero-width split dodged the mask: %q", e.Cmd)
	}
	// Control characters can't reach the UI.
	e = good(Event{Agent: "a\x1b[2J\nb", Kind: "cmd-end", Cwd: "/x\x00y"})
	if strings.ContainsAny(e.Agent+e.Cwd, "\x1b\n\x00") {
		t.Errorf("control chars: %q %q", e.Agent, e.Cwd)
	}
}

func TestColor(t *testing.T) {
	if Color("x") != Color("x") {
		t.Fatal("not stable")
	}
	pal := map[string]bool{"#2457c5": true, "#1e8a5a": true, "#9a4fb5": true, "#b7791f": true, "#c2410c": true, "#0e7490": true}
	for _, id := range []string{"", "x", "inside:claude", "alice", "bob", "日本語"} {
		if !pal[Color(id)] {
			t.Errorf("Color(%q) = %q", id, Color(id))
		}
	}
	// Same as the web's avatarColor: "alice" -> h = 92903040 ... checked by hand below.
	if got := Color("a"); got != "#1e8a5a" { // 97 % 6 = 1
		t.Errorf("Color(a) = %q", got)
	}
}

func FuzzSanitize(f *testing.F) {
	f.Add([]byte(`{"agent":"claude","kind":"screen","screen":{"action":"click","pos":[1,2]}}`))
	f.Add([]byte(`{"agent":"x","kind":"cmd-start","id":"1","cmd":"a --token b"}`))
	f.Add([]byte(`{"agent":"x","kind":"screen","screen":{"action":"type","chars":-1,"keys":["a"]}}`))
	f.Fuzz(func(t *testing.T, b []byte) {
		var e Event
		if json.Unmarshal(b, &e) != nil {
			return
		}
		out, ok := Sanitize(e)
		if !ok {
			return
		}
		if utf8.RuneCountInString(out.Agent) > 40 || utf8.RuneCountInString(out.Cmd) > 121 ||
			utf8.RuneCountInString(out.Cwd) > 200 || utf8.RuneCountInString(out.ID) > 32 {
			t.Fatalf("over cap: %+v", out)
		}
		if out.Screen != nil {
			s := out.Screen
			if len(s.Pos) != posLen[s.Action] || len(s.Keys) > 8 || s.N < 0 || s.N > 100 || s.Chars < 0 || s.Chars > 1_000_000 {
				t.Fatalf("bad screen: %+v", s)
			}
			for _, p := range s.Pos {
				if p < 0 || p > 65535 {
					t.Fatalf("pos: %v", s.Pos)
				}
			}
		}
		if again, ok2 := Sanitize(out); !ok2 || !jsonEq(again, out) {
			t.Fatalf("not idempotent: %+v vs %+v", out, again)
		}
	})
}

func jsonEq(a, b Event) bool {
	x, _ := json.Marshal(a)
	y, _ := json.Marshal(b)
	return string(x) == string(y)
}
