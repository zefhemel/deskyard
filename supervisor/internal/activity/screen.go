package activity

import (
	"path"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"
)

// ScreenCommands are the deskyard subcommands that act on the screen.
var ScreenCommands = []string{"click", "double-click", "triple-click", "mouse-down", "mouse-up", "move", "drag", "scroll", "type", "key", "hold-key", "screenshot", "zoom"}

// ScreenAction is a screen action as recorded. It never carries typed text,
// only how much was typed.
type ScreenAction struct {
	Action string   `json:"action"`
	Pos    []int    `json:"pos,omitempty"` // [x y], or [x1 y1 x2 y2] for drag
	Button string   `json:"button,omitempty"`
	Dir    string   `json:"dir,omitempty"`
	N      int      `json:"n,omitempty"`
	Keys   []string `json:"keys,omitempty"`
	Chars  int      `json:"chars,omitempty"` // type: character count only
}

var (
	buttons  = []string{"left", "right", "middle"}
	scrollTo = []string{"up", "down", "left", "right"}
	intRE    = regexp.MustCompile(`^[0-9]+$`)
	numRE    = regexp.MustCompile(`^[0-9]+([.][0-9]+)?$`)
)

// ParseScreenArgv recognises a `deskyard` command line that acts on the
// screen: argv[0]'s base name is deskyard, the global -d/--desktop option may
// come first, and the first other argument is a screen command whose
// arguments are valid. Anything else (a shell wrapper, exec, help) is not.
func ParseScreenArgv(argv []string) (ScreenAction, bool) {
	if len(argv) < 2 || path.Base(argv[0]) != "deskyard" {
		return ScreenAction{}, false
	}
	if slices.ContainsFunc(argv[1:], func(a string) bool { return a == "-h" || a == "--help" }) {
		return ScreenAction{}, false
	}
	rest := argv[1:]
	for len(rest) > 0 {
		a := rest[0]
		switch {
		case a == "-d" || a == "--desktop":
			if len(rest) < 2 {
				return ScreenAction{}, false
			}
			rest = rest[2:]
			continue
		case strings.HasPrefix(a, "--desktop="), strings.HasPrefix(a, "-c="):
			rest = rest[1:]
			continue
		}
		break
	}
	if len(rest) == 0 {
		return ScreenAction{}, false
	}
	return ParseScreenArgs(rest[0], rest[1:])
}

// ParseScreenArgs checks a screen command's arguments the way the CLI does
// (internal/cli/desktop.Validate) and returns the action to record.
func ParseScreenArgs(action string, args []string) (ScreenAction, bool) {
	if !slices.Contains(ScreenCommands, action) {
		return ScreenAction{}, false
	}
	a := ScreenAction{Action: action}
	var ok bool
	switch action {
	case "screenshot":
		return a, true
	case "zoom":
		p, good := coords(args, 4)
		if !good || p[2] <= p[0] || p[3] <= p[1] {
			return ScreenAction{}, false
		}
		return a, true
	case "move", "double-click", "triple-click":
		if a.Pos, ok = coords(args, 2); !ok {
			return ScreenAction{}, false
		}
	case "click":
		if a.Pos, ok = coords(args, 2); !ok {
			return ScreenAction{}, false
		}
		if a.Button, ok = button(args[2:]); !ok {
			return ScreenAction{}, false
		}
	case "mouse-down", "mouse-up":
		if a.Button, ok = button(args); !ok {
			return ScreenAction{}, false
		}
	case "drag":
		if a.Pos, ok = coords(args, 4); !ok {
			return ScreenAction{}, false
		}
	case "scroll":
		if a.Pos, ok = coords(args, 2); !ok || len(args) < 3 || !slices.Contains(scrollTo, args[2]) {
			return ScreenAction{}, false
		}
		a.Pos, a.Dir, a.N = a.Pos[:2], args[2], 3
		if len(args) > 3 {
			n, good := atoi(args[3])
			if !good {
				return ScreenAction{}, false
			}
			a.N = n
		}
	case "type":
		if len(args) < 1 {
			return ScreenAction{}, false
		}
		// Only how much was typed: the arguments are joined with spaces.
		for i, s := range args {
			a.Chars += utf8.RuneCountInString(s)
			if i > 0 {
				a.Chars++
			}
		}
	case "key":
		if len(args) < 1 {
			return ScreenAction{}, false
		}
		a.Keys = slices.Clone(args)
	case "hold-key":
		if len(args) < 2 || !numRE.MatchString(args[1]) {
			return ScreenAction{}, false
		}
		a.Keys = []string{args[0]}
	}
	return a, true
}

// coords reads the first n arguments as whole non-negative numbers.
func coords(args []string, n int) ([]int, bool) {
	if len(args) < n {
		return nil, false
	}
	out := make([]int, n)
	for i := range out {
		v, ok := atoi(args[i])
		if !ok {
			return nil, false
		}
		out[i] = v
	}
	return out, true
}

func atoi(s string) (int, bool) {
	if !intRE.MatchString(s) {
		return 0, false
	}
	v, err := strconv.Atoi(s)
	return v, err == nil
}

// button reads an optional button argument; the default is left.
func button(args []string) (string, bool) {
	if len(args) == 0 || args[0] == "" {
		return "left", true
	}
	return args[0], slices.Contains(buttons, args[0])
}
