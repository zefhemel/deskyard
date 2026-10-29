package activity

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestParseScreenArgv(t *testing.T) {
	ok := []struct {
		argv []string
		want ScreenAction
	}{
		{[]string{"deskyard", "click", "10", "20"}, ScreenAction{Action: "click", Pos: []int{10, 20}, Button: "left"}},
		{[]string{"/usr/local/bin/deskyard", "-c", "alice", "drag", "1", "2", "3", "4"}, ScreenAction{Action: "drag", Pos: []int{1, 2, 3, 4}}},
		{[]string{"deskyard", "--computer=alice", "scroll", "5", "6", "down"}, ScreenAction{Action: "scroll", Pos: []int{5, 6}, Dir: "down", N: 3}},
		{[]string{"deskyard", "--computer", "a", "scroll", "5", "6", "up", "7"}, ScreenAction{Action: "scroll", Pos: []int{5, 6}, Dir: "up", N: 7}},
		{[]string{"deskyard", "-c=alice", "click", "1", "2"}, ScreenAction{Action: "click", Pos: []int{1, 2}, Button: "left"}},
		{[]string{"deskyard", "double-click", "7", "8"}, ScreenAction{Action: "double-click", Pos: []int{7, 8}}},
		{[]string{"deskyard", "triple-click", "7", "8"}, ScreenAction{Action: "triple-click", Pos: []int{7, 8}}},
		{[]string{"deskyard", "move", "7", "8"}, ScreenAction{Action: "move", Pos: []int{7, 8}}},
		{[]string{"deskyard", "type", "hunter2", "x"}, ScreenAction{Action: "type", Chars: 9}},
		{[]string{"deskyard", "key", "ctrl+l", "Return"}, ScreenAction{Action: "key", Keys: []string{"ctrl+l", "Return"}}},
		{[]string{"deskyard", "hold-key", "shift", "0.5"}, ScreenAction{Action: "hold-key", Keys: []string{"shift"}}},
		{[]string{"deskyard", "screenshot", "-"}, ScreenAction{Action: "screenshot"}},
		{[]string{"deskyard", "screenshot"}, ScreenAction{Action: "screenshot"}},
		{[]string{"deskyard", "zoom", "0", "0", "10", "10", "-"}, ScreenAction{Action: "zoom"}},
		{[]string{"deskyard", "mouse-down", "right"}, ScreenAction{Action: "mouse-down", Button: "right"}},
		{[]string{"deskyard", "mouse-up"}, ScreenAction{Action: "mouse-up", Button: "left"}},
		{[]string{"deskyard", "click", "1", "2", "middle"}, ScreenAction{Action: "click", Pos: []int{1, 2}, Button: "middle"}},
	}
	for _, c := range ok {
		got, good := ParseScreenArgv(c.argv)
		if !good || !reflect.DeepEqual(got, c.want) {
			t.Errorf("ParseScreenArgv(%q) = %+v, %v; want %+v", c.argv, got, good, c.want)
		}
	}
	bad := [][]string{
		{"sh", "-c", "deskyard click 1 2"}, {"deskyard", "exec", "--", "click"}, {"deskyard", "click", "x", "y"},
		{"deskyard", "wait", "1"}, {"deskyard", "click", "--help"}, {"desk", "click", "1", "2"}, {},
		{"deskyard"}, {"deskyard", "click", "1"}, {"deskyard", "click", "-1", "2"},
		{"deskyard", "click", "1", "2", "purple"}, {"deskyard", "scroll", "1", "2", "sideways"},
		{"deskyard", "zoom", "5", "5", "1", "1"}, {"deskyard", "type"}, {"deskyard", "key"},
		{"deskyard", "hold-key", "shift", "soon"}, {"deskyard", "click", "99999999999999999999", "2"},
		{"deskyard", "-h", "click", "1", "2"}, {"deskyard", "--bogus", "click", "1", "2"},
		{"deskyard", "-c"}, {"deskyard", "-c=", "click", "1", "2"}, {"deskyard", "--computer=", "click", "1", "2"},
		{"deskyard", "-e", "x", "click", "1", "2"}, {"deskyard", "--environment=x", "click", "1", "2"},
		{"deskyard", "-d", "alice", "click", "1", "2"}, {"deskyard", "--desktop", "alice", "click", "1", "2"},
		{"deskyard", "--desktop=alice", "click", "1", "2"},
	}
	for _, argv := range bad {
		if got, good := ParseScreenArgv(argv); good {
			t.Errorf("ParseScreenArgv(%q) = %+v, true; want false", argv, got)
		}
	}
}

func TestTypeNeverRecordsText(t *testing.T) {
	a, ok := ParseScreenArgv([]string{"deskyard", "type", "hunter2", "x"})
	if !ok {
		t.Fatal("not ok")
	}
	b, _ := json.Marshal(a)
	if strings.Contains(string(b), "hunter2") {
		t.Fatalf("typed text leaked: %s", b)
	}
}

func FuzzParseScreenArgv(f *testing.F) {
	f.Add("deskyard\x00click\x0010\x0020")
	f.Add("deskyard\x00type\x00secret")
	f.Fuzz(func(t *testing.T, s string) {
		argv := strings.Split(s, "\x00")
		if a, ok := ParseScreenArgv(argv); ok {
			b, _ := json.Marshal(a)
			if a.Action == "type" && a.Pos != nil {
				t.Fatalf("type with pos: %s", b)
			}
		}
	})
}
