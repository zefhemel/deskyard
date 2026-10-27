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

	"github.com/zefhemel/deskyard/supervisor/internal/activity"
	"github.com/zefhemel/deskyard/supervisor/internal/cli/remote"
)

// App holds everything the commands touch, so tests can swap it.
type App struct {
	Stdin          io.Reader
	Stdout, Stderr io.Writer
	Getenv         func(string) string
	// ConfigPath overrides servers.json's location (tests).
	ConfigPath string
	// Inside reports whether this process runs inside an environment, where
	// commands act locally when no remote is chosen.
	Inside  func() bool
	HTTP    *http.Client
	Version string
	// Record sends an activity event (default activity.Send); Caller names
	// the program running this command (default: from the process tree).
	Record func(activity.Event)
	Caller func() string
	// OpenBrowser opens a URL in the user's web browser (deskyard login); an
	// error means it couldn't, and the URL is only printed.
	OpenBrowser func(url string) error

	callerName string
	callerSet  bool

	envFlag string // -e/--environment: ENV or SERVER/ENV
}

// New returns an App wired to the process.
func New(version string) *App {
	return &App{
		Stdin: os.Stdin, Stdout: os.Stdout, Stderr: os.Stderr,
		Getenv:      os.Getenv,
		Inside:      insideEnvironment,
		Version:     version,
		Record:      activity.Send,
		Caller:      defaultCaller(os.Getenv),
		OpenBrowser: openBrowser(os.Getenv),
	}
}

// insideEnvironment: environment images create /etc/workspace (see
// images/Dockerfile) and run Linux.
func insideEnvironment() bool {
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
			fmt.Fprintf(a.Stderr, "deskyard: %s\nRun `deskyard --help` for the list of commands, or `deskyard guide` to get started.\n", ue.msg)
		}
		return 2
	}
	if errors.Is(err, context.Canceled) {
		fmt.Fprintln(a.Stderr, "deskyard: interrupted")
		return 130
	}
	fmt.Fprintf(a.Stderr, "deskyard: %v\n", err)
	return 1
}

func (a *App) run(ctx context.Context, argv []string) error {
	// Global flags, only before the command name: -e is exec's --env after it.
	for len(argv) > 0 {
		arg := argv[0]
		switch {
		case arg == "-e" || arg == "--environment":
			if len(argv) < 2 || argv[1] == "" {
				return usagef(nil, "%s needs an environment: deskyard -e ENV COMMAND, or -e SERVER/ENV (see `deskyard server list`)", arg)
			}
			if _, _, err := splitSelector(argv[1]); err != nil {
				return usagef(nil, "%s: %v", arg, err)
			}
			a.envFlag, argv = argv[1], argv[2:]
			continue
		case strings.HasPrefix(arg, "-e="), strings.HasPrefix(arg, "--environment="):
			flag, v, _ := strings.Cut(arg, "=")
			if v == "" {
				return usagef(nil, "%s needs an environment: deskyard -e ENV COMMAND, or -e SERVER/ENV (see `deskyard server list`)", flag)
			}
			if _, _, err := splitSelector(v); err != nil {
				return usagef(nil, "%s: %v", flag, err)
			}
			a.envFlag, argv = v, argv[1:]
			continue
		case arg == "-h" || arg == "--help":
			return a.printHelp(nil)
		case arg == "--version":
			return runVersion(a, nil)
		case arg == "-d" || arg == "--desktop" || strings.HasPrefix(arg, "--desktop="):
			name, _, _ := strings.Cut(arg, "=")
			return usagef(nil, "unknown option %q (choose an environment with -e ENV before the command)", name)
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
		if argv[0] == "remote" {
			return usagef(nil, "unknown command \"remote\": remotes were replaced by servers you sign in to; run `deskyard login https://host/ENV` (or `deskyard auth LINK`), then `deskyard server list`")
		}
		if argv[0] == "server" || argv[0] == "env" || argv[0] == "browser" {
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
		// Desktop (screen) arguments are free text (type, key), so only a lone
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
	for _, c := range visibleCommands() {
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

// configPath is where servers.json lives.
func (a *App) configPath() (string, error) {
	if a.ConfigPath != "" {
		return a.ConfigPath, nil
	}
	if dir := a.Getenv("XDG_CONFIG_HOME"); dir != "" {
		return dir + string(os.PathSeparator) + "deskyard" + string(os.PathSeparator) + "servers.json", nil
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

const loginHint = "run `deskyard login https://host/ENV` (or `deskyard auth LINK` with a login link)"

// splitSelector splits -e's ENV or SERVER/ENV.
func splitSelector(sel string) (server, env string, err error) {
	if sel == "" {
		return "", "", nil
	}
	server, env, ok := strings.Cut(sel, "/")
	if !ok {
		return "", sel, nil
	}
	if server == "" || env == "" || strings.Contains(env, "/") {
		return "", "", fmt.Errorf("%q isn't an environment: write ENV, or SERVER/ENV for another server", sel)
	}
	return server, env, nil
}

// selector is the environment chosen with -e, else DESKYARD_ENVIRONMENT.
func (a *App) selector() (string, error) {
	if a.envFlag != "" {
		return a.envFlag, nil
	}
	sel := a.Getenv("DESKYARD_ENVIRONMENT")
	if _, _, err := splitSelector(sel); err != nil {
		return "", fmt.Errorf("DESKYARD_ENVIRONMENT: %v", err)
	}
	return sel, nil
}

// target picks the server and environment to talk to: -e, else
// DESKYARD_ENVIRONMENT, else the default server's environment. It returns
// nil, nil when the command should act locally (inside an environment with
// no environment chosen).
func (a *App) target() (*remote.Client, error) {
	sel, err := a.selector()
	if err != nil {
		return nil, err
	}
	return a.resolve(sel)
}

// resolve finds the client for ENV or SERVER/ENV ("" = the default
// server's environment).
func (a *App) resolve(sel string) (*remote.Client, error) {
	name, env, err := splitSelector(sel)
	if err != nil {
		return nil, err
	}
	srv, err := a.server(name)
	if err != nil {
		return nil, err
	}
	if env == "" && srv != nil {
		env = srv.Environment
	}
	if sel == "" && env == "" && a.Inside != nil && a.Inside() {
		return nil, nil
	}
	if srv == nil {
		return nil, a.noServer()
	}
	if env == "" {
		return nil, fmt.Errorf("no environment chosen on %s: use -e ENV, or choose a default with `deskyard env use ENV`", srv.Name)
	}
	c := remote.FromServer(srv, env)
	c.HTTP = a.HTTP
	return c, nil
}

// server returns the named server, or for "" the default one: the server
// DESKYARD_URL names, else servers.json's default (nil when there is none).
func (a *App) server(name string) (*remote.Server, error) {
	if name == "" {
		if srv, err := a.envServer(); srv != nil || err != nil {
			return srv, err
		}
	}
	cfg, _, err := a.loadConfig()
	if err != nil {
		return nil, err
	}
	if name == "" {
		return cfg.Servers[cfg.Default], nil
	}
	if srv := cfg.Servers[name]; srv != nil {
		return srv, nil
	}
	if len(cfg.Servers) == 0 {
		return nil, fmt.Errorf("no server named %q: you aren't signed in to any; %s", name, loginHint)
	}
	var names []string
	for _, s := range cfg.Sorted() {
		names = append(names, s.Name)
	}
	return nil, fmt.Errorf("no server named %q (there are: %s): run `deskyard server list`, or sign in with `deskyard login https://host/ENV --name %s`", name, strings.Join(names, ", "), name)
}

// noServer explains why there is no default server.
func (a *App) noServer() error {
	cfg, _, err := a.loadConfig()
	if err != nil {
		return err
	}
	if len(cfg.Servers) > 0 {
		return errors.New("no server chosen: run `deskyard server list`, then `deskyard server default NAME`, or use -e SERVER/ENV")
	}
	return errors.New("not signed in to any server: " + loginHint)
}

// envServer is the server DESKYARD_URL and DESKYARD_CREDENTIAL describe, or
// nil when DESKYARD_URL isn't set.
func (a *App) envServer() (*remote.Server, error) {
	u := a.Getenv("DESKYARD_URL")
	if u == "" {
		return nil, nil
	}
	t, err := remote.ParseURL(u)
	if err != nil {
		return nil, fmt.Errorf("DESKYARD_URL: %v", err)
	}
	cred := a.Getenv("DESKYARD_CREDENTIAL")
	if cred == "" {
		return nil, errors.New("DESKYARD_URL is set but DESKYARD_CREDENTIAL isn't: set it to a client credential (dyc_…) from `deskyard login` or `deskyard auth`")
	}
	return &remote.Server{Name: remote.DefaultName(t.Base), URL: t.Base, Credential: cred, Environment: t.Environment}, nil
}
