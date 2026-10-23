//go:build linux || darwin

package term

import (
	"os"
	"os/signal"
	"syscall"

	"golang.org/x/sys/unix"
)

type state struct{ termios unix.Termios }

// Supported reports whether raw terminals work on this platform.
const Supported = true

// IsTerminal reports whether fd is a terminal.
func IsTerminal(fd int) bool {
	_, err := unix.IoctlGetTermios(fd, ioctlGetTermios)
	return err == nil
}

// Save captures terminal attributes without changing them.
func Save(fd int) (*State, error) {
	t, err := unix.IoctlGetTermios(fd, ioctlGetTermios)
	if err != nil {
		return nil, err
	}
	return &State{state{*t}}, nil
}

// MakeRaw puts fd into raw mode and returns the previous state.
func MakeRaw(fd int) (*State, error) {
	t, err := unix.IoctlGetTermios(fd, ioctlGetTermios)
	if err != nil {
		return nil, err
	}
	old := &State{state{*t}}
	// cfmakeraw
	t.Iflag &^= unix.IGNBRK | unix.BRKINT | unix.PARMRK | unix.ISTRIP | unix.INLCR | unix.IGNCR | unix.ICRNL | unix.IXON
	t.Oflag &^= unix.OPOST
	t.Lflag &^= unix.ECHO | unix.ECHONL | unix.ICANON | unix.ISIG | unix.IEXTEN
	t.Cflag &^= unix.CSIZE | unix.PARENB
	t.Cflag |= unix.CS8
	t.Cc[unix.VMIN] = 1
	t.Cc[unix.VTIME] = 0
	if err := unix.IoctlSetTermios(fd, ioctlSetTermios, t); err != nil {
		return nil, err
	}
	return old, nil
}

// Restore puts the terminal back.
func Restore(fd int, s *State) error {
	return unix.IoctlSetTermios(fd, ioctlSetTermios, &s.termios)
}

// Size returns the terminal's columns and rows.
func Size(fd int) (cols, rows int, err error) {
	ws, err := unix.IoctlGetWinsize(fd, unix.TIOCGWINSZ)
	if err != nil {
		return 0, 0, err
	}
	return int(ws.Col), int(ws.Row), nil
}

// NotifyResize sends on c whenever the terminal is resized (SIGWINCH);
// call the returned function to stop.
func NotifyResize(c chan<- os.Signal) (stop func()) {
	signal.Notify(c, syscall.SIGWINCH)
	return func() { signal.Stop(c) }
}
