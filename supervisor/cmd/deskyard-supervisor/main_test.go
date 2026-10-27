package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCheckDataDir(t *testing.T) {
	if err := checkDataDir(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(t.TempDir(), "f")
	if err := os.WriteFile(file, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{file, filepath.Join(t.TempDir(), "missing")} {
		err := checkDataDir(p)
		if err == nil || !strings.Contains(err.Error(), "-v <host folder>:/data") {
			t.Fatalf("%s: %v", p, err)
		}
	}
}
