// Package app is the `deskyard` command: argument parsing, help, and the
// commands themselves.
package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"runtime"
	"sort"
	"strings"

	"github.com/zefhemel/deskyard/supervisor/internal/cli/remote"
)

// App holds everything the commands touch, so tests can swap it.
type App struct {
	Stdin          io.Reader
	Stdout, Stderr io.Writer
	Getenv         func(string) string
	// ConfigPath overrides remotes.json's location (tests).
	ConfigPath string
	// Inside reports whether this process runs inside a desktop, where
	// commands act locally when no remote is chosen.
	Inside  func() bool
	HTTP    *http.Client
	Version string

	remoteFlag string // -c
}

// New returns an App wired to the process.
func New(version string) *App {
	return &App{
		Stdin: os.Stdin, Stdout: os.Stdout, Stderr: os.Stderr,
		Getenv:  os.Getenv,
		Inside:  insideDesktop,
		Version: version,
	}
}

// insideDesktop: desktop images create /etc/workspace (see
// images/debian-xfce/Dockerfile) and run Linux.
func insideDesktop() bool {
	if runtime.GOOS != "linux" {
		return false
	}
	fi, err := os.Stat("/etc/workspace")
	return err == nil && fi.IsDir()
}

// usageError makes the command exit 2.
type usageError struct {
	cmd *Command
	msg string
}

func (e *usageError) Error() string { return e.msg }

func usagef(cmd *Command, format string, args ...any) error {
	return &usageError{cmd: cmd, msg: fmt.Sprintf(format, args...)}
}

// exitCode ends the command with a code and no message (the command's own
// output already said what happened).
type exitCode int

func (e exitCode) Error() string { return fmt.Sprintf("exit %d", int(e)) }

// call is one parsed invocation.
type call struct {
	cmd   *Command
	ctx   context.Context
	args  []string
	flags map[string][]string
}

func (c *call) bool(name string) bool { return len(c.flags[name]) > 0 }

func (c *call) str(name, def string) string {
	if v := c.flags[name]; len(v) > 0 {
		return v[len(v)-1]
	}
	return def
}

// Run runs the command line (without the program name) and returns the
// exit code.
func (a *App) Run(ctx context.Context, argv []string) int {
	err := a.run(ctx, argv)
	if err == nil {
		return 0
	}
	var ec exitCode
	if errors.As(err, &ec) {
		return int(ec)
	}
	var ue *usageError
	if errors.As(err, &ue) {
		if ue.cmd != nil {
			fmt.Fprintf(a.Stderr, "deskyard %s: %s\nusage: deskyard %s\nRun `deskyard %s --help` for details.\n", ue.cmd.Name, ue.msg, ue.cmd.Usage, ue.cmd.Name)
		} else {
			fmt.Fprintf(a.Stderr, "desktop: %s\nRun `deskyard --help` for the list of commands, or `deskyard guide` to get started.\n", ue.msg)
		}
		return 2
	}
	if errors.Is(err, context.Canceled) {
		fmt.Fprintln(a.Stderr, "desktop: interrupted")
		return 130
	}
	fmt.Fprintf(a.Stderr, "desktop: %v\n", err)
	return 1
}

func (a *App) run(ctx context.Context, argv []string) error {
	// Global flags.
	for len(argv) > 0 {
		arg := argv[0]
		switch {
		case arg == "-d" || arg == "--desktop":
			if len(argv) < 2 || argv[1] == "" {
				return usagef(nil, "-d needs a remote name: deskyard -d NAME COMMAND (see `deskyard remote list`)")
			}
			a.remoteFlag, argv = argv[1], argv[2:]
			continue
		case strings.HasPrefix(arg, "-c="), strings.HasPrefix(arg, "--desktop="):
			_, v, _ := strings.Cut(arg, "=")
			a.remoteFlag, argv = v, argv[1:]
			continue
		case arg == "-h" || arg == "--help":
			return a.printHelp(nil)
		case arg == "--version":
			return runVersion(a, nil)
		case strings.HasPrefix(arg, "-") && arg != "-":
			return usagef(nil, "unknown option %q (options for a command go after it, e.g. deskyard exec --json -- ls)", arg)
		}
		break
	}
	if len(argv) == 0 {
		a.writeOverview(a.Stderr)
		return exitCode(2)
	}

	cmd, rest := lookup(argv)
	if cmd == nil {
		if argv[0] == "remote" || argv[0] == "browser" {
			if len(argv) == 1 {
				return usagef(nil, "%s needs a subcommand: %s", argv[0], strings.Join(subcommands(argv[0]), ", "))
			}
			if argv[1] == "-h" || argv[1] == "--help" {
				return a.printHelp([]string{argv[0]})
			}
			return usagef(nil, "unknown command %q: %s has %s", argv[0]+" "+argv[1], argv[0], strings.Join(subcommands(argv[0]), ", "))
		}
		return usagef(nil, "unknown command %q", argv[0])
	}

	c := &call{cmd: cmd, ctx: ctx, flags: map[string][]string{}}
	if cmd.Group == "desktop" {
		// Desktop arguments are free text (type, key), so only a lone
		// --help is help.
		if len(rest) == 1 && (rest[0] == "--help" || rest[0] == "-h") {
			return a.printHelp(strings.Fields(cmd.Name))
		}
		c.args = rest
	} else if cmd.rawArgs {
		for _, arg := range rest {
			if arg == "--help" || arg == "-h" {
				return a.printHelp(strings.Fields(cmd.Name))
			}
		}
		c.args = rest
	} else {
		help, err := parseFlags(c, rest)
		if err != nil {
			return err
		}
		if help {
			return a.printHelp(strings.Fields(cmd.Name))
		}
	}
	return cmd.run(a, c)
}

// lookup finds the longest command name matching the start of argv.
func lookup(argv []string) (*Command, []string) {
	if len(argv) >= 2 {
		if c := findCommand(argv[0] + " " + argv[1]); c != nil {
			return c, argv[2:]
		}
	}
	if c := findCommand(argv[0]); c != nil {
		return c, argv[1:]
	}
	return nil, nil
}

func subcommands(parent string) []string {
	var out []string
	for _, c := range commands {
		if p, sub, ok := strings.Cut(c.Name, " "); ok && p == parent {
			out = append(out, sub)
		}
	}
	sort.Strings(out)
	return out
}

func (cmd *Command) flag(name string) *Flag {
	for i := range cmd.Flags {
		f := &cmd.Flags[i]
		if f.Name == name || (f.Short != "" && f.Short == name) {
			return f
		}
	}
	return nil
}

// parseFlags fills c.flags and c.args; it reports whether --help was asked.
func parseFlags(c *call, argv []string) (help bool, err error) {
	cmd := c.cmd
	for i := 0; i < len(argv); i++ {
		arg := argv[i]
		if arg == "--" {
			c.args = append(c.args, argv[i+1:]...)
			break
		}
		if arg == "-h" || arg == "--help" {
			return true, nil
		}
		if !strings.HasPrefix(arg, "-") || arg == "-" {
			if cmd.stopAtArg {
				c.args = append(c.args, argv[i:]...)
				break
			}
			c.args = append(c.args, arg)
			continue
		}
		name, val, hasVal := strings.Cut(arg, "=")
		f := cmd.flag(name)
		if f == nil {
			return false, usagef(cmd, "unknown option %q", name)
		}
		if f.Type == "boolean" {
			if hasVal {
				return false, usagef(cmd, "%s takes no value", f.Name)
			}
			c.flags[f.Name] = append(c.flags[f.Name], "true")
			continue
		}
		if !hasVal {
			if i+1 >= len(argv) {
				return false, usagef(cmd, "%s needs a value (%s)", f.Name, f.Value)
			}
			i++
			val = argv[i]
		}
		c.flags[f.Name] = append(c.flags[f.Name], val)
	}
	// Argument count from the spec.
	required, max := 0, 0
	for _, a := range cmd.Args {
		if a.Required {
			required++
		}
		if a.Variadic {
			max = -1
		} else if max >= 0 {
			max++
		}
	}
	if len(c.args) < required {
		var names []string
		for _, a := range cmd.Args {
			if a.Required {
				names = append(names, strings.ToUpper(a.Name))
			}
		}
		return false, usagef(cmd, "missing %s", strings.Join(names[len(c.args):], " "))
	}
	if max >= 0 && len(c.args) > max {
		return false, usagef(cmd, "too many arguments (unexpected %q)", c.args[max])
	}
	return false, nil
}

// configPath is where remotes.json lives.
func (a *App) configPath() (string, error) {
	if a.ConfigPath != "" {
		return a.ConfigPath, nil
	}
	if dir := a.Getenv("XDG_CONFIG_HOME"); dir != "" {
		return dir + string(os.PathSeparator) + "deskyard" + string(os.PathSeparator) + "remotes.json", nil
	}
	return remote.DefaultPath()
}

func (a *App) loadConfig() (*remote.Config, string, error) {
	p, err := a.configPath()
	if err != nil {
		return nil, "", err
	}
	cfg, err := remote.Load(p)
	return cfg, p, err
}

const addHint = "No remotes yet? Get a token on the desktop's Agents tab, then run `deskyard remote add NAME https://host/NAME --token dyt_…`"

// target picks the remote to talk to. It returns nil, nil when the command
// should act locally (inside a desktop with no remote chosen).
func (a *App) target() (*remote.Client, error) {
	name := a.remoteFlag
	if name == "" {
		name = a.Getenv("DESKYARD_REMOTE")
	}
	if name != "" {
		return a.namedRemote(name)
	}
	if u := a.Getenv("DESKYARD_URL"); u != "" {
		return a.envRemote(u)
	}
	cfg, _, err := a.loadConfig()
	if err != nil {
		return nil, err
	}
	if cfg.Default != "" && cfg.Remotes[cfg.Default] != nil {
		return a.client(cfg.Remotes[cfg.Default]), nil
	}
	if a.Inside != nil && a.Inside() {
		return nil, nil
	}
	if len(cfg.Remotes) > 0 {
		return nil, errors.New("no remote chosen: run `deskyard remote list`, then use -d NAME (or set DESKYARD_REMOTE=NAME, or `deskyard remote default NAME`)")
	}
	return nil, errors.New("no remote chosen: run `deskyard remote list`, then use -d NAME. " + addHint)
}

func (a *App) namedRemote(name string) (*remote.Client, error) {
	cfg, _, err := a.loadConfig()
	if err != nil {
		return nil, err
	}
	if r := cfg.Remotes[name]; r != nil {
		return a.client(r), nil
	}
	// DESKYARD_URL's desktop can be named too (cp alice:/x).
	if u := a.Getenv("DESKYARD_URL"); u != "" {
		if c, err := a.envRemote(u); err == nil && c.Desktop == name {
			return c, nil
		}
	}
	if len(cfg.Remotes) == 0 {
		return nil, fmt.Errorf("no remote named %q. %s", name, addHint)
	}
	var names []string
	for _, r := range cfg.Sorted() {
		names = append(names, r.Name)
	}
	return nil, fmt.Errorf("no remote named %q (there are: %s): run `deskyard remote list`, or add it with `deskyard remote add %s https://host/%s --token dyt_…`", name, strings.Join(names, ", "), name, name)
}

func (a *App) envRemote(u string) (*remote.Client, error) {
	t, err := remote.ParseURL(u)
	if err != nil {
		return nil, fmt.Errorf("DESKYARD_URL: %v", err)
	}
	tok := a.Getenv("DESKYARD_TOKEN")
	if tok == "" {
		return nil, errors.New("DESKYARD_URL is set but DESKYARD_TOKEN isn't: set it to the agent token (dyt_…) for that desktop")
	}
	return &remote.Client{Alias: t.Desktop, Base: t.Base, Desktop: t.Desktop, Token: tok, HTTP: a.HTTP}, nil
}

func (a *App) client(r *remote.Remote) *remote.Client {
	c := remote.FromRemote(r)
	c.HTTP = a.HTTP
	return c
}
