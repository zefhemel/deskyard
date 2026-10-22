package store

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
)

func TestRoundTripAndMode(t *testing.T) {
	p := filepath.Join(t.TempDir(), "sub", "x.json")
	if err := WriteJSON(p, map[string]int{"a": 1}); err != nil {
		t.Fatal(err)
	}
	var got map[string]int
	if err := ReadJSON(p, &got); err != nil || got["a"] != 1 {
		t.Fatalf("%v %v", err, got)
	}
	st, _ := os.Stat(p)
	if st.Mode().Perm() != 0o600 {
		t.Fatalf("mode %v", st.Mode())
	}
	entries, _ := os.ReadDir(filepath.Dir(p))
	if len(entries) != 1 {
		t.Fatalf("temporary files left: %v", entries)
	}
}

func TestMissing(t *testing.T) {
	var v any
	if err := ReadJSON(filepath.Join(t.TempDir(), "nope.json"), &v); !errors.Is(err, fs.ErrNotExist) {
		t.Fatal(err)
	}
}
