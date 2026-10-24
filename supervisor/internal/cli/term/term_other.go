//go:build !linux && !darwin

package term

import (
	"errors"
	"os"
)

type state struct{}

// Supported reports whether raw terminals work on this platform.
const Supported = false

var errUnsupported = errors.New("interactive terminals aren't supported on this platform yet")

// IsTerminal reports whether fd is a terminal (never, here).
func IsTerminal(fd int) bool { return false }

// Save is unsupported here.
func Save(fd int) (*State, error) { return nil, errUnsupported }

// MakeRaw is unsupported here.
func MakeRaw(fd int) (*State, error) { return nil, errUnsupported }

// Restore is unsupported here.
func Restore(fd int, s *State) error { return errUnsupported }

// Size is unsupported here.
func Size(fd int) (cols, rows int, err error) { return 0, 0, errUnsupported }

// NotifyResize never fires here.
func NotifyResize(c chan<- os.Signal) (stop func()) { return func() {} }
