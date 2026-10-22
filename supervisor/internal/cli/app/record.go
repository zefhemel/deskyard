package app

import (
	"crypto/rand"
	"encoding/hex"
	"io/fs"
	"os"
	"strconv"
	"time"

	"github.com/zefhemel/deskyard/supervisor/internal/activity"
)

// defaultCaller names the program that ran this `deskyard` from the process
// tree.
func defaultCaller(getenv func(string) string) func() string {
	return func() string { return callerFrom(os.DirFS("/proc"), os.Getppid(), getenv) }
}

// callerFrom reads the process tree; only when the tree can't be read at all
// does CLAUDECODE=1 (set by Claude Code for everything it runs) name Claude
// Code. It never overrides what the tree says: a terminal or program started
// from a Claude Code command keeps its own name.
func callerFrom(proc fs.FS, pid int, getenv func(string) string) string {
	if _, err := fs.Stat(proc, strconv.Itoa(pid)+"/stat"); err != nil && getenv("CLAUDECODE") == "1" {
		return "Claude Code"
	}
	return activity.CallerName(proc, pid)
}

// record sends one event to the desktop's activity stream. It is for the
// local (inside a desktop) path only, never waits and never fails: nothing
// here may slow or break the command that is running.
func (a *App) record(e activity.Event) {
	if a.Record == nil || a.Getenv("DESKYARD_ACTIVITY") == "off" {
		return
	}
	defer func() { _ = recover() }()
	if !a.callerSet {
		a.callerSet = true
		if a.Caller != nil {
			a.callerName = a.Caller()
		}
	}
	if a.callerName == "" {
		return
	}
	e.Agent = a.callerName
	e.T = time.Now()
	a.Record(e)
}

// recordScreen records a screen action (click, key, ...); commands that don't
// act on the screen (size, cursor, wait) record nothing.
func (a *App) recordScreen(action string, args []string) {
	if s, ok := activity.ParseScreenArgs(action, args); ok {
		a.record(activity.Event{Kind: "screen", Screen: &s})
	}
}

// recordCommand records the start of a command and returns the function that
// records its end: the exit code, or 1 when it couldn't run.
func (a *App) recordCommand(argv []string) (end func(code int, err error)) {
	if a.Record == nil || a.Getenv("DESKYARD_ACTIVITY") == "off" {
		return func(int, error) {}
	}
	var b [4]byte
	_, _ = rand.Read(b[:])
	id := hex.EncodeToString(b[:])
	a.record(activity.Event{Kind: "cmd-start", ID: id, Cmd: activity.DisplayCommand(argv)})
	return func(code int, err error) {
		if err != nil {
			code = 1
		}
		a.record(activity.Event{Kind: "cmd-end", ID: id, Exit: &code})
	}
}
