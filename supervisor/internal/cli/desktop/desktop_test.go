package desktop

import (
	"errors"
	"reflect"
	"testing"
)

func TestValidate(t *testing.T) {
	ok := [][]string{
		{"screenshot"}, {"screenshot", "x.png"}, {"zoom", "0", "0", "10", "10"}, {"zoom", "1", "2", "3", "4", "-"},
		{"size"}, {"cursor"}, {"move", "1", "2"}, {"click", "1", "2"}, {"click", "1", "2", "right"},
		{"double-click", "1", "2"}, {"triple-click", "1", "2"}, {"mouse-down"}, {"mouse-up", "middle"},
		{"drag", "1", "2", "3", "4"}, {"type", "hello", "world"}, {"key", "ctrl+a", "BackSpace"},
		{"hold-key", "shift", "0.2"}, {"scroll", "1", "2", "down"}, {"scroll", "1", "2", "left", "5"}, {"wait", "1"}, {"wait", "0.5"},
	}
	for _, c := range ok {
		if err := Validate(c[0], c[1:]); err != nil {
			t.Errorf("%v: %v", c, err)
		}
	}
	bad := [][]string{
		{"bogus"}, {"click", "1"}, {"click", "1", "x"}, {"click", "-1", "2"}, {"click", "1", "2", "up"},
		{"zoom", "50", "50", "10", "10"}, {"zoom", "0", "0", "0", "10"}, {"zoom", "1", "2", "3"},
		{"move"}, {"mouse-down", "4"}, {"drag", "1", "2", "3"}, {"type"}, {"key"}, {"hold-key", "a"},
		{"hold-key", "a", "x"}, {"scroll", "1", "2", "sideways"}, {"scroll", "1", "2", "up", "1.5"}, {"wait"}, {"wait", "-1"},
	}
	for _, c := range bad {
		err := Validate(c[0], c[1:])
		var ue *UsageError
		if !errors.As(err, &ue) {
			t.Errorf("%v: want a usage error, got %v", c, err)
		}
	}
}

func TestOutputFile(t *testing.T) {
	f, rest := OutputFile("zoom", []string{"1", "2", "3", "4", "z.png"})
	if f != "z.png" || !reflect.DeepEqual(rest, []string{"1", "2", "3", "4"}) {
		t.Errorf("zoom: %q %v", f, rest)
	}
	f, rest = OutputFile("screenshot", nil)
	if f != "" || len(rest) != 0 {
		t.Errorf("screenshot: %q %v", f, rest)
	}
}
