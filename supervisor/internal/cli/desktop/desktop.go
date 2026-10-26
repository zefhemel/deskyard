// Package desktop implements the screen actions (screenshot, click, type,
// …) with xdotool and maim on the desktop's display, and checks their
// arguments for local and remote use alike.
package desktop

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Actions lists the screen actions in help order.
var Actions = []string{
	"screenshot", "zoom", "size", "cursor", "move", "click", "double-click", "triple-click",
	"mouse-down", "mouse-up", "drag", "type", "key", "hold-key", "scroll", "wait",
}

// IsAction reports whether name is a screen action.
func IsAction(name string) bool {
	for _, a := range Actions {
		if a == name {
			return true
		}
	}
	return false
}

// UsageError is a bad argument; the command exits 2.
type UsageError struct{ Msg string }

func (e *UsageError) Error() string { return e.Msg }

func usagef(format string, args ...any) error { return &UsageError{fmt.Sprintf(format, args...)} }

var (
	intRE = regexp.MustCompile(`^[0-9]+$`)
	numRE = regexp.MustCompile(`^[0-9]+([.][0-9]+)?$`)
)

func need(args []string, n int, what string) error {
	if len(args) < n {
		return usagef("missing arguments: expected %s", what)
	}
	return nil
}

func ints(args ...string) error {
	for _, a := range args {
		if !intRE.MatchString(a) {
			return usagef("%q is not a coordinate: use whole, non-negative pixel numbers", a)
		}
	}
	return nil
}

func button(s string) (string, error) {
	switch s {
	case "", "left":
		return "1", nil
	case "middle":
		return "2", nil
	case "right":
		return "3", nil
	}
	return "", usagef("unknown mouse button %q: use left, right or middle", s)
}

func arg(args []string, i int) string {
	if i < len(args) {
		return args[i]
	}
	return ""
}

// Validate checks an action's arguments the way the original bash command
// did; a problem is a *UsageError.
func Validate(action string, args []string) error {
	switch action {
	case "screenshot", "size", "cursor":
		return nil
	case "zoom":
		if err := need(args, 4, "X0 Y0 X1 Y1"); err != nil {
			return err
		}
		if err := ints(args[:4]...); err != nil {
			return err
		}
		x0, _ := strconv.Atoi(args[0])
		y0, _ := strconv.Atoi(args[1])
		x1, _ := strconv.Atoi(args[2])
		y1, _ := strconv.Atoi(args[3])
		if x1 <= x0 || y1 <= y0 {
			return usagef("the region is empty: X1 must be greater than X0 and Y1 greater than Y0 (top-left corner first, then bottom-right)")
		}
	case "move", "double-click", "triple-click":
		if err := need(args, 2, "X Y"); err != nil {
			return err
		}
		return ints(args[0], args[1])
	case "click":
		if err := need(args, 2, "X Y [left|right|middle]"); err != nil {
			return err
		}
		if err := ints(args[0], args[1]); err != nil {
			return err
		}
		_, err := button(arg(args, 2))
		return err
	case "mouse-down", "mouse-up":
		_, err := button(arg(args, 0))
		return err
	case "drag":
		if err := need(args, 4, "X1 Y1 X2 Y2"); err != nil {
			return err
		}
		return ints(args[:4]...)
	case "type":
		return need(args, 1, "TEXT")
	case "key":
		return need(args, 1, "at least one key, like ctrl+l or Return")
	case "hold-key":
		if err := need(args, 2, "KEY SECONDS"); err != nil {
			return err
		}
		if !numRE.MatchString(args[1]) {
			return usagef("%q is not a number of seconds", args[1])
		}
	case "wait":
		if err := need(args, 1, "SECONDS"); err != nil {
			return err
		}
		if !numRE.MatchString(args[0]) {
			return usagef("%q is not a number of seconds", args[0])
		}
	case "scroll":
		if err := need(args, 3, "X Y up|down|left|right [N]"); err != nil {
			return err
		}
		if err := ints(args[0], args[1]); err != nil {
			return err
		}
		if _, err := scrollButton(args[2]); err != nil {
			return err
		}
		if len(args) > 3 && !intRE.MatchString(args[3]) {
			return usagef("%q is not a number of notches", args[3])
		}
	default:
		return usagef("unknown screen action %q", action)
	}
	return nil
}

func scrollButton(dir string) (string, error) {
	switch dir {
	case "up":
		return "4", nil
	case "down":
		return "5", nil
	case "left":
		return "6", nil
	case "right":
		return "7", nil
	}
	return "", usagef("unknown scroll direction %q: use up, down, left or right", dir)
}

// OutputFile returns the FILE argument of screenshot/zoom ("" or "-" mean
// stdout) and the arguments without it.
func OutputFile(action string, args []string) (file string, rest []string) {
	switch action {
	case "screenshot":
		return arg(args, 0), nil
	case "zoom":
		return arg(args, 4), args[:4]
	}
	return "", args
}

// Env is what the local actions need from the outside world.
type Env struct {
	Stdout, Stderr io.Writer
	Getenv         func(string) string
}

func settle(getenv func(string) string) error {
	s := getenv("DESKYARD_SETTLE")
	if s == "" {
		s = "0.3"
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil || f < 0 {
		return fmt.Errorf("DESKYARD_SETTLE=%q is not a number of seconds", s)
	}
	time.Sleep(time.Duration(f * float64(time.Second)))
	return nil
}

func sleepSeconds(s string) {
	f, _ := strconv.ParseFloat(s, 64)
	time.Sleep(time.Duration(f * float64(time.Second)))
}

// ExitError carries a tool's exit code.
type ExitError struct{ Code int }

func (e *ExitError) Error() string { return fmt.Sprintf("exit status %d", e.Code) }

func (e Env) run(ctx context.Context, stdout io.Writer, name string, args ...string) error {
	cmd := exec.CommandContext(ctx, name, args...)
	env := os.Environ()
	if e.Getenv("DISPLAY") == "" {
		env = append(env, "DISPLAY=:1")
	}
	cmd.Env = env
	cmd.Stdout, cmd.Stderr = stdout, e.Stderr
	err := cmd.Run()
	var xe *exec.ExitError
	if errors.As(err, &xe) {
		return &ExitError{Code: xe.ExitCode()}
	}
	if errors.Is(err, exec.ErrNotFound) {
		return fmt.Errorf("%s is not installed: screen actions only work inside an environment, or remotely with -e NAME", name)
	}
	return err
}

func (e Env) xdotool(ctx context.Context, args ...string) error {
	return e.run(ctx, e.Stdout, "xdotool", args...)
}

func (e Env) png(ctx context.Context, file string, geometry ...string) error {
	args := append([]string{"--format=png"}, geometry...)
	if file == "" || file == "-" {
		return e.run(ctx, e.Stdout, "maim", append(args, "/dev/stdout")...)
	}
	return e.run(ctx, e.Stdout, "maim", append(args, file)...)
}

// RunLocal performs an action on this desktop's display. Arguments must
// have passed Validate.
func (e Env) RunLocal(ctx context.Context, action string, args []string) error {
	switch action {
	case "screenshot":
		if err := settle(e.Getenv); err != nil {
			return err
		}
		return e.png(ctx, arg(args, 0))
	case "zoom":
		x0, _ := strconv.Atoi(args[0])
		y0, _ := strconv.Atoi(args[1])
		x1, _ := strconv.Atoi(args[2])
		y1, _ := strconv.Atoi(args[3])
		if err := settle(e.Getenv); err != nil {
			return err
		}
		return e.png(ctx, arg(args, 4), "-g", fmt.Sprintf("%dx%d+%d+%d", x1-x0, y1-y0, x0, y0))
	case "size":
		return e.xdotool(ctx, "getdisplaygeometry")
	case "cursor":
		var out bytes.Buffer
		if err := e.run(ctx, &out, "xdotool", "getmouselocation", "--shell"); err != nil {
			return err
		}
		vals := map[string]string{}
		sc := bufio.NewScanner(&out)
		for sc.Scan() {
			if k, v, ok := strings.Cut(sc.Text(), "="); ok {
				vals[k] = v
			}
		}
		_, err := fmt.Fprintf(e.Stdout, "%s %s\n", vals["X"], vals["Y"])
		return err
	case "move":
		return e.xdotool(ctx, "mousemove", args[0], args[1])
	case "click":
		b, _ := button(arg(args, 2))
		return e.xdotool(ctx, "mousemove", args[0], args[1], "click", b)
	case "double-click":
		return e.xdotool(ctx, "mousemove", args[0], args[1], "click", "--repeat", "2", "--delay", "80", "1")
	case "triple-click":
		return e.xdotool(ctx, "mousemove", args[0], args[1], "click", "--repeat", "3", "--delay", "80", "1")
	case "mouse-down":
		b, _ := button(arg(args, 0))
		return e.xdotool(ctx, "mousedown", b)
	case "mouse-up":
		b, _ := button(arg(args, 0))
		return e.xdotool(ctx, "mouseup", b)
	case "drag":
		return e.xdotool(ctx, "mousemove", args[0], args[1], "mousedown", "1", "mousemove", args[2], args[3], "mouseup", "1")
	case "type":
		return e.xdotool(ctx, "type", "--delay", "12", "--", strings.Join(args, " "))
	case "key":
		return e.xdotool(ctx, append([]string{"key", "--delay", "30", "--"}, args...)...)
	case "hold-key":
		if err := e.xdotool(ctx, "keydown", "--", args[0]); err != nil {
			return err
		}
		sleepSeconds(args[1])
		return e.xdotool(ctx, "keyup", "--", args[0])
	case "wait":
		sleepSeconds(args[0])
		return nil
	case "scroll":
		b, _ := scrollButton(args[2])
		n := "3"
		if len(args) > 3 {
			n = args[3]
		}
		return e.xdotool(ctx, "mousemove", args[0], args[1], "click", "--repeat", n, b)
	}
	return usagef("unknown screen action %q", action)
}
