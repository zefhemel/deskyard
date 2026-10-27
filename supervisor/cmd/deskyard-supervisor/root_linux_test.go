//go:build linux

package main

import (
	"os"
	"testing"
)

// Unprivileged, dropping must fail loudly rather than carry on as someone
// else; as root it would change this test process, so it is skipped.
func TestDropPrivilegesRefusedWithoutRoot(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("would drop this test process's privileges")
	}
	if err := dropPrivileges(os.Getgid(), true); err == nil {
		t.Fatal("expected an error when not root")
	}
	if err := dropPrivileges(0, false); err == nil {
		t.Fatal("expected an error when not root and without a socket")
	}
}
