package app

// The command tree as data: help, `describe --json` and flag parsing all
// come from here.

// Arg is a positional argument.
type Arg struct {
	Name     string   `json:"name"`
	Type     string   `json:"type"` // integer, number, string, enum, path, remote-path
	Required bool     `json:"required"`
	Variadic bool     `json:"variadic,omitempty"`
	Enum     []string `json:"enum,omitempty"`
	Default  string   `json:"default,omitempty"`
	Desc     string   `json:"description"`
}

// Flag is an option.
type Flag struct {
	Name    string `json:"name"`            // --json
	Short   string `json:"short,omitempty"` // -t
	Type    string `json:"type"`            // boolean, string, integer, string[] (repeatable)
	Value   string `json:"value,omitempty"` // metavar: DIR
	Default string `json:"default,omitempty"`
	Desc    string `json:"description"`
}

// Output describes what a command prints on stdout.
type Output struct {
	Type   string            `json:"type"` // none, text, png, json, bytes, stream
	Desc   string            `json:"description"`
	Fields map[string]string `json:"fields,omitempty"` // for json/text: field -> meaning
}

// Command is one (sub)command.
type Command struct {
	Name     string   `json:"name"` // "click", "server list"
	Group    string   `json:"group"`
	Summary  string   `json:"summary"`
	Usage    string   `json:"usage"` // after "deskyard "
	Desc     string   `json:"description"`
	Args     []Arg    `json:"arguments"`
	Flags    []Flag   `json:"flags"`
	Output   Output   `json:"output"`
	Examples []string `json:"examples"`
	Where    string   `json:"where"` // where it runs
	Exit     string   `json:"exitCodes"`
	// Hidden commands are left out of help, guide and describe: the image
	// runs them, not people or agents.
	Hidden bool `json:"-"`

	stopAtArg bool // flags end at the first argument (exec)
	rawArgs   bool // the command parses its own arguments (forward: -R applies to the next one)
	run       func(a *App, c *call) error
}

const (
	whereBoth   = "inside an environment it acts locally; elsewhere on the chosen environment (-e ENV)"
	whereLocal  = "on this machine (no server needed)"
	whereServer = "on this machine, talking to the server"
	whereRemote = "on the chosen environment (-e ENV); inside an environment without one chosen it acts locally"
	exitStd     = "0 ok, 1 error, 2 usage error"
	exitDesktop = "0 ok, 1 error, 2 usage error; otherwise the exit code of the action on the desktop"
	exitExec    = "the command's own exit code; 1 when it couldn't run (connection, credential, stopped environment), 2 usage error"
)

var (
	argX     = Arg{Name: "x", Type: "integer", Required: true, Desc: "horizontal pixel position, 0 = left edge"}
	argY     = Arg{Name: "y", Type: "integer", Required: true, Desc: "vertical pixel position, 0 = top edge"}
	buttons  = []string{"left", "right", "middle"}
	flagJSON = Flag{Name: "--json", Type: "boolean", Desc: "print JSON instead of text"}
	pngOut   = Output{Type: "png", Desc: "PNG image bytes on stdout, or nothing when FILE is given (the file is written on this machine)"}
	noOutput = Output{Type: "none", Desc: "nothing"}
)

// Groups in help order.
var groups = []struct{ Name, Title string }{
	{"desktop", "Desktop (screen, mouse, keyboard)"},
	{"shell", "Shell and files"},
	{"browser", "Browser"},
	{"servers", "Servers and sign-in"},
	{"info", "Information"},
}

var commands []*Command

func init() {
	commands = []*Command{
		// Desktop
		{Name: "screenshot", Group: "desktop", Summary: "PNG of the whole screen",
			Usage:  "screenshot [FILE|-]",
			Desc:   "Takes a PNG screenshot of the whole screen, after a short settle delay (DESKYARD_SETTLE seconds, default 0.3) so the screen reflects your last action. Without FILE, or with -, the PNG goes to stdout. Take one before acting and after, to see what happened; coordinates in the image are screen coordinates.",
			Args:   []Arg{{Name: "file", Type: "path", Desc: "where to write the PNG on this machine; - or omitted for stdout"}},
			Output: pngOut, Examples: []string{"deskyard screenshot shot.png", "deskyard screenshot > shot.png"},
			Where: whereBoth, Exit: exitDesktop, run: runDesktop},
		{Name: "zoom", Group: "desktop", Summary: "PNG of a region at full resolution",
			Usage: "zoom X0 Y0 X1 Y1 [FILE|-]",
			Desc:  "Takes a PNG of just the region from the top-left corner X0 Y0 to the bottom-right corner X1 Y1 (exclusive), at full resolution: use it to read small text or check details. X1 must be greater than X0 and Y1 greater than Y0.",
			Args: []Arg{{Name: "x0", Type: "integer", Required: true, Desc: "left edge"}, {Name: "y0", Type: "integer", Required: true, Desc: "top edge"},
				{Name: "x1", Type: "integer", Required: true, Desc: "right edge (greater than x0)"}, {Name: "y1", Type: "integer", Required: true, Desc: "bottom edge (greater than y0)"},
				{Name: "file", Type: "path", Desc: "where to write the PNG on this machine; - or omitted for stdout"}},
			Output: pngOut, Examples: []string{"deskyard zoom 100 50 420 290 detail.png"},
			Where: whereBoth, Exit: exitDesktop, run: runDesktop},
		{Name: "size", Group: "desktop", Summary: "print the screen size",
			Usage: "size", Desc: "Prints the screen size in pixels.",
			Output:   Output{Type: "text", Desc: `one line "W H", e.g. "1280 800"`},
			Examples: []string{"deskyard size"}, Where: whereBoth, Exit: exitDesktop, run: runDesktop},
		{Name: "cursor", Group: "desktop", Summary: "print the mouse position",
			Usage: "cursor", Desc: "Prints where the mouse pointer is.",
			Output:   Output{Type: "text", Desc: `one line "X Y", e.g. "640 400"`},
			Examples: []string{"deskyard cursor"}, Where: whereBoth, Exit: exitDesktop, run: runDesktop},
		{Name: "move", Group: "desktop", Summary: "move the mouse",
			Usage: "move X Y", Desc: "Moves the mouse pointer to X Y without clicking (to hover, e.g. for tooltips or menus).",
			Args: []Arg{argX, argY}, Output: noOutput, Examples: []string{"deskyard move 640 400"},
			Where: whereBoth, Exit: exitDesktop, run: runDesktop},
		{Name: "click", Group: "desktop", Summary: "click at X Y",
			Usage: "click X Y [left|right|middle]", Desc: "Moves the mouse to X Y and clicks a button (default left).",
			Args:   []Arg{argX, argY, {Name: "button", Type: "enum", Enum: buttons, Default: "left", Desc: "mouse button"}},
			Output: noOutput, Examples: []string{"deskyard click 640 400", "deskyard click 640 400 right"},
			Where: whereBoth, Exit: exitDesktop, run: runDesktop},
		{Name: "double-click", Group: "desktop", Summary: "double left-click at X Y",
			Usage: "double-click X Y", Desc: "Double-clicks the left button at X Y (selects a word, opens a file).",
			Args: []Arg{argX, argY}, Output: noOutput, Examples: []string{"deskyard double-click 200 300"},
			Where: whereBoth, Exit: exitDesktop, run: runDesktop},
		{Name: "triple-click", Group: "desktop", Summary: "triple left-click at X Y",
			Usage: "triple-click X Y", Desc: "Triple-clicks the left button at X Y (selects a line or paragraph).",
			Args: []Arg{argX, argY}, Output: noOutput, Examples: []string{"deskyard triple-click 200 300"},
			Where: whereBoth, Exit: exitDesktop, run: runDesktop},
		{Name: "mouse-down", Group: "desktop", Summary: "press a mouse button",
			Usage: "mouse-down [left|right|middle]", Desc: "Presses a mouse button at the current position and keeps it down (with move and mouse-up, for custom drags).",
			Args:   []Arg{{Name: "button", Type: "enum", Enum: buttons, Default: "left", Desc: "mouse button"}},
			Output: noOutput, Examples: []string{"deskyard move 100 100 && deskyard mouse-down && deskyard move 300 300 && deskyard mouse-up"},
			Where: whereBoth, Exit: exitDesktop, run: runDesktop},
		{Name: "mouse-up", Group: "desktop", Summary: "release a mouse button",
			Usage: "mouse-up [left|right|middle]", Desc: "Releases a mouse button pressed with mouse-down.",
			Args:   []Arg{{Name: "button", Type: "enum", Enum: buttons, Default: "left", Desc: "mouse button"}},
			Output: noOutput, Examples: []string{"deskyard mouse-up"},
			Where: whereBoth, Exit: exitDesktop, run: runDesktop},
		{Name: "drag", Group: "desktop", Summary: "left-drag from one point to another",
			Usage: "drag X1 Y1 X2 Y2", Desc: "Presses the left button at X1 Y1, moves to X2 Y2 and releases it.",
			Args: []Arg{{Name: "x1", Type: "integer", Required: true, Desc: "start x"}, {Name: "y1", Type: "integer", Required: true, Desc: "start y"},
				{Name: "x2", Type: "integer", Required: true, Desc: "end x"}, {Name: "y2", Type: "integer", Required: true, Desc: "end y"}},
			Output: noOutput, Examples: []string{"deskyard drag 100 100 400 300"},
			Where: whereBoth, Exit: exitDesktop, run: runDesktop},
		{Name: "type", Group: "desktop", Summary: "type text into the focused window",
			Usage: "type TEXT...", Desc: "Types text into the focused window, as keystrokes. Several arguments are joined with spaces. Click the field first; use key for Return, Tab or shortcuts.",
			Args:   []Arg{{Name: "text", Type: "string", Required: true, Variadic: true, Desc: "text to type"}},
			Output: noOutput, Examples: []string{"deskyard type 'hello world'"},
			Where: whereBoth, Exit: exitDesktop, run: runDesktop},
		{Name: "key", Group: "desktop", Summary: "press keys or shortcuts",
			Usage: "key COMBO...", Desc: "Presses keys one after another, in xdotool syntax: Return, Escape, Tab, BackSpace, Up, F5, ctrl+l, ctrl+shift+t, super.",
			Args:   []Arg{{Name: "combo", Type: "string", Required: true, Variadic: true, Desc: "a key or key combination"}},
			Output: noOutput, Examples: []string{"deskyard key ctrl+l", "deskyard key ctrl+a BackSpace"},
			Where: whereBoth, Exit: exitDesktop, run: runDesktop},
		{Name: "hold-key", Group: "desktop", Summary: "hold a key down for a while",
			Usage: "hold-key KEY SECONDS", Desc: "Holds a key down for SECONDS, then releases it.",
			Args:   []Arg{{Name: "key", Type: "string", Required: true, Desc: "key in xdotool syntax"}, {Name: "seconds", Type: "number", Required: true, Desc: "how long"}},
			Output: noOutput, Examples: []string{"deskyard hold-key shift 0.5"},
			Where: whereBoth, Exit: exitDesktop, run: runDesktop},
		{Name: "scroll", Group: "desktop", Summary: "scroll at X Y",
			Usage: "scroll X Y up|down|left|right [N]", Desc: "Moves the mouse to X Y and scrolls N notches (default 3) in a direction.",
			Args: []Arg{argX, argY, {Name: "direction", Type: "enum", Required: true, Enum: []string{"up", "down", "left", "right"}, Desc: "scroll direction"},
				{Name: "n", Type: "integer", Default: "3", Desc: "number of notches"}},
			Output: noOutput, Examples: []string{"deskyard scroll 640 400 down 5"},
			Where: whereBoth, Exit: exitDesktop, run: runDesktop},
		{Name: "wait", Group: "desktop", Summary: "pause for a while",
			Usage: "wait SECONDS", Desc: "Pauses for SECONDS, e.g. for a page or app to load before the next screenshot.",
			Args:   []Arg{{Name: "seconds", Type: "number", Required: true, Desc: "how long"}},
			Output: noOutput, Examples: []string{"deskyard wait 2"},
			Where: whereBoth, Exit: exitDesktop, run: runDesktop},

		// Shell and files
		{Name: "exec", Group: "shell", Summary: "run a command in the environment",
			Usage: "exec [OPTIONS] -- CMD [ARG...]",
			Desc:  "Runs one command as user (in /workspace unless --cwd) and streams its stdin, stdout and stderr; exits with the command's exit code. The command is run as given, not through a shell: for pipes, globs or && use `-- bash -lc '...'`.",
			Args:  []Arg{{Name: "cmd", Type: "string", Required: true, Variadic: true, Desc: "the command and its arguments"}},
			Flags: []Flag{
				{Name: "--json", Type: "boolean", Desc: `buffer the output and print one JSON object {"stdout","stderr","exit"} instead of streaming; exit 0 when the JSON was printed`},
				{Name: "--tty", Short: "-t", Type: "boolean", Desc: "run in a terminal (for programs that need one); stdout and stderr are merged"},
				{Name: "--cwd", Type: "string", Value: "DIR", Default: "/workspace", Desc: "working directory in the environment"},
				{Name: "--env", Short: "-e", Type: "string[]", Value: "K=V", Desc: "after exec: set an environment variable for the command (repeatable); not the global -e ENV before exec, which chooses the environment"},
			},
			Output: Output{Type: "stream", Desc: `the command's stdout and stderr as they arrive; with --json one object {"stdout":string,"stderr":string,"exit":int}, using "stdoutBase64"/"stderrBase64" (base64) instead when the output isn't UTF-8`,
				Fields: map[string]string{"stdout": "standard output (UTF-8)", "stderr": "standard error (UTF-8)", "exit": "exit code", "stdoutBase64": "standard output, base64, when not UTF-8", "stderrBase64": "standard error, base64, when not UTF-8"}},
			Examples: []string{"deskyard -e alice exec -- ls -la", "deskyard -e alice exec -e CI=1 -- make test", "deskyard exec --json -- bash -lc 'cd app && npm test'", "echo hi | deskyard exec -- tee /workspace/hi.txt"},
			Where:    whereRemote, Exit: exitExec, stopAtArg: true, run: runExec},
		{Name: "terminal", Group: "shell", Summary: "join a shared terminal session",
			Usage: "terminal [SESSION]", Desc: "Joins a shared Bash login shell in /workspace, default main. Browser, CLI and desktop clients share the same session. Inside an existing native shared terminal, press Ctrl-B then s and choose the session instead of nesting this command. Detach with Ctrl-B then D; jobs survive disconnect but end when the environment restarts. Session names use 1–64 letters, digits, underscores or dashes and start with a letter or digit.",
			Args: []Arg{{Name: "session", Type: "string", Default: "main", Desc: "shared session name"}}, Output: Output{Type: "stream", Desc: "shared terminal input and output"},
			Examples: []string{"deskyard -e alice terminal", "deskyard terminal build-2"}, Where: whereRemote, Exit: exitExec, run: runTerminal},
		{Name: "terminal-new", Group: "shell", Hidden: true, Examples: []string{"deskyard terminal-new"}, Summary: "open an independent shared terminal", Desc: "Internal native terminal default, preserving the current directory.", Usage: "terminal-new", Where: whereLocal, Exit: exitExec, Output: noOutput, run: runTerminalRuntime},
		{Name: "terminal-list", Group: "shell", Hidden: true, Examples: []string{"deskyard terminal-list"}, Summary: "list shared terminal sessions", Desc: "Internal bounded session discovery.", Usage: "terminal-list", Where: whereLocal, Exit: exitStd, Output: noOutput, run: runTerminalRuntime},
		{Name: "terminal-pick", Group: "shell", Hidden: true, Examples: []string{"deskyard terminal-pick"}, Summary: "choose a shared terminal session", Desc: "Internal desktop Terminal launcher: lists running sessions to join, or opens a new one like terminal-new.", Usage: "terminal-pick", Where: whereLocal, Exit: exitExec, Output: noOutput, run: runTerminalRuntime},
		{Name: "terminal-create", Group: "shell", Hidden: true, Examples: []string{"deskyard terminal-create"}, Summary: "start a detached shared terminal", Desc: "Internal: starts the next free terminal-N in /workspace without attaching and prints its name.", Usage: "terminal-create", Where: whereLocal, Exit: exitStd, Output: noOutput, run: runTerminalRuntime},
		{Name: "terminal-kill", Group: "shell", Hidden: true, Examples: []string{"deskyard terminal-kill terminal-2"}, Summary: "end a shared terminal session", Desc: "Internal: ends the session for every client, including browser attachments.", Usage: "terminal-kill SESSION", Where: whereLocal, Exit: exitStd + ", 3 no such session", Output: noOutput, Args: []Arg{{Name: "session", Type: "string", Required: true}}, run: runTerminalRuntime},
		{Name: "terminal-lease", Group: "shell", Hidden: true, Examples: []string{"deskyard terminal-lease main 0123456789abcdef0123456789abcdef join"}, Summary: "own a shared terminal attachment", Desc: "Internal non-TTY attachment lifetime lease. With join it never creates the session: a missing one prints ended and exits 3.", Usage: "terminal-lease SESSION ATTACHMENT [join]", Where: whereLocal, Exit: exitExec, Output: noOutput, Args: []Arg{{Name: "session", Type: "string", Required: true}, {Name: "attachment", Type: "string", Required: true}, {Name: "mode", Type: "enum", Enum: []string{"join"}}}, run: runTerminalRuntime},
		{Name: "terminal-attach", Group: "shell", Hidden: true, Examples: []string{"deskyard terminal-attach 0123456789abcdef0123456789abcdef"}, Summary: "join a prepared terminal attachment", Desc: "Internal TTY attachment to an existing lease.", Usage: "terminal-attach ATTACHMENT", Where: whereLocal, Exit: exitExec, Output: noOutput, Args: []Arg{{Name: "attachment", Type: "string", Required: true}}, run: runTerminalRuntime},
		{Name: "terminal-helper", Group: "shell", Hidden: true, Examples: []string{"deskyard terminal-helper main"}, Summary: "attach the image shared terminal", Desc: "Internal shared terminal attachment.", Usage: "terminal-helper SESSION", Where: whereLocal, Exit: exitExec, Output: noOutput, Args: []Arg{{Name: "session", Type: "string", Required: true}}, run: runTerminal},
		{Name: "shell", Group: "shell", Summary: "interactive login shell",
			Usage: "shell", Desc: "Opens an interactive bash login shell in the environment in your terminal (raw mode, resizes follow your window). Exit with `exit` or Ctrl-D. For scripted use, prefer exec.",
			Output:   Output{Type: "stream", Desc: "the terminal session"},
			Examples: []string{"deskyard -e alice shell"},
			Where:    whereRemote, Exit: "the shell's exit code; 1 when it couldn't start", run: runShell},
		{Name: "cp", Group: "shell", Summary: "copy files to or from an environment",
			Usage: "cp SRC DST",
			Desc: "Copies a file or directory between this machine and an environment; write the environment side as ENV:/path (ENV is an environment on the chosen server, -e SERVER/… or the default one; an empty ENV, as in :/path, means the chosen environment). " +
				"A file lands at DST, or inside DST when it is an existing directory. A directory's contents land in DST, which is created (like `cp -r SRC/. DST`). " +
				"SRC - reads stdin; DST - writes to stdout (a tar stream for a directory). Files land as user; inside an environment, two local paths are copied locally.",
			Args:     []Arg{{Name: "src", Type: "path", Required: true, Desc: "local path, -, or ENV:/path"}, {Name: "dst", Type: "path", Required: true, Desc: "local path, -, or ENV:/path"}},
			Output:   Output{Type: "none", Desc: "nothing, unless DST is - (the file's bytes or a tar stream)"},
			Examples: []string{"deskyard cp ./report.pdf alice:/workspace/", "deskyard cp ./site alice:/workspace/site", "deskyard cp alice:/workspace/out.csv ."},
			Where:    whereRemote, Exit: exitStd, run: runCp},

		{Name: "forward", Group: "shell", Summary: "map TCP ports between this machine and the environment",
			Usage: "forward [-R] PORT[:PORT]...",
			Desc: "Maps TCP ports both ways until Ctrl-C, over the remote access API (no port is opened anywhere else). Each argument is one mapping. " +
				"PORT or LOCAL:ENV: this machine's 127.0.0.1:LOCAL becomes localhost:ENV in the environment (a dev server here, reachable by the environment's browser). " +
				"-R PORT or -R ENV:LOCAL (reverse; one -R before each such mapping): the environment's localhost:ENV becomes 127.0.0.1:LOCAL here. " +
				"Both ends listen on loopback only; the environment won't listen below 1024 (plain forward only; -R just dials, so -R 80:8080 works). A port in use on either side fails that mapping, which is retried, and the others carry on. " +
				"When the connection drops (the environment stops or restarts, the network blips) it keeps its local listeners, closes open connections and reconnects with backoff (1 s up to 30 s). " +
				"It exits only on Ctrl-C, or when the credential is refused. Run it in the background. Inside an environment it needs -e too.",
			Args:  []Arg{{Name: "port", Type: "string", Required: true, Variadic: true, Desc: "a mapping: PORT or LOCAL:ENV; after -R, PORT or ENV:LOCAL"}},
			Flags: []Flag{{Name: "--reverse", Short: "-R", Type: "boolean", Desc: "the next mapping is reverse: a port in the environment becomes a port here"}},
			Output: Output{Type: "text", Desc: `one line per mapping each time it goes live: "in LOCAL ENV" or "out ENV 127.0.0.1:LOCAL"; on stderr one line per state change (linked, lost and retrying, port in use)`,
				Fields: map[string]string{"in": "LOCAL ENV: this machine's port, reachable in the environment at localhost:ENV", "out": "ENV ADDR: the environment's port, reachable here at ADDR"}},
			Examples: []string{"deskyard -e alice forward 3000 -R 5173", "deskyard -e alice forward 8080:3000 &", "deskyard -e alice forward -R 9229:19229"},
			Where:    "on the chosen environment (-e ENV); inside an environment too, since it maps ports between two machines. Ports are LOCAL:ENV, or ENV:LOCAL after -R. The environment won't listen below 1024. \"Linked to ...\" and the out lines appear when the link is accepted, before the environment side is known to run", Exit: "0 after Ctrl-C, 1 when the credential is refused (401) or no environment is chosen, 2 usage error", rawArgs: true, run: runForward},

		// Browser
		{Name: "browser connect", Group: "browser", Summary: "forward the environment's browser to a local CDP port",
			Usage: "browser connect [--port N]",
			Desc: "Listens on 127.0.0.1:PORT and forwards the Chrome DevTools Protocol to the environment's shared Chromium (the one on its desktop, with your signed-in sessions), so Playwright, Puppeteer or DevTools tools connect as if the browser were local. " +
				"Prints the URL, then keeps running until Ctrl-C. Run it in the background. Inside an environment it isn't needed: CDP is at http://127.0.0.1:9221.",
			Flags:    []Flag{{Name: "--port", Type: "integer", Value: "N", Default: "9222", Desc: "local port (0 = any free port)"}},
			Output:   Output{Type: "text", Desc: "first line: the local CDP URL (http://127.0.0.1:9222); connection log on stderr"},
			Examples: []string{"deskyard -e alice browser connect &", "# then in Python: p.chromium.connect_over_cdp(\"http://127.0.0.1:9222\")"},
			Where:    whereRemote, Exit: exitStd, run: runBrowserConnect},

		// Servers and sign-in
		{Name: "login", Group: "servers", Summary: "sign in to a server, approving in the browser",
			Usage: "login URL [OPTIONS]",
			Desc: "Signs this CLI in to a Deskyard server: prints a code and an approval URL (on stderr) and opens it in your browser, where you check the code and approve while signed in to Deskyard. " +
				"Then saves the server, your username and a client credential for this machine in servers.json ($XDG_CONFIG_HOME/deskyard, default ~/.config/deskyard; mode 0600). " +
				"With an environment in the URL (https://host/alice) it becomes the server's default environment, once checked that you have full access to it; without one, your only environment does. The first server becomes the default. The request expires after 10 minutes.",
			Args: []Arg{{Name: "url", Type: "string", Required: true, Desc: "the server's address, optionally with an environment: https://host or https://host/alice"}},
			Flags: []Flag{{Name: "--name", Type: "string", Value: "NAME", Desc: "local name for the server, used with -e NAME/ENV (default: its host name)"},
				{Name: "--no-browser", Type: "boolean", Desc: "only print the approval URL, don't open a browser"}},
			Output:   Output{Type: "text", Desc: "a confirmation with the account and environment; the code and approval URL on stderr"},
			Examples: []string{"deskyard login https://deskyard.example.com/alice", "deskyard login https://deskyard.example.com --name work --no-browser"},
			Where:    whereServer, Exit: exitStd, run: runLogin},
		{Name: "auth", Group: "servers", Summary: "sign in to a server with a login link",
			Usage:    "auth LINK [--name NAME]",
			Desc:     "Signs this CLI in with a login link (https://host/link#dyl_…), without a browser: the link is used up, and the server, your username and a client credential are saved in servers.json as for login. When the account has exactly one environment it becomes the default. Observe-only links work in a browser only.",
			Args:     []Arg{{Name: "link", Type: "string", Required: true, Desc: "the whole login link, https://host/link#dyl_…"}},
			Flags:    []Flag{{Name: "--name", Type: "string", Value: "NAME", Desc: "local name for the server, used with -e NAME/ENV (default: its host name)"}},
			Output:   Output{Type: "text", Desc: "a confirmation with the account and environment"},
			Examples: []string{"deskyard auth 'https://deskyard.example.com/link#dyl_…'"},
			Where:    whereServer, Exit: exitStd, run: runAuth},
		{Name: "logout", Group: "servers", Summary: "revoke this CLI's credential and forget the server",
			Usage:  "logout [SERVER]",
			Desc:   "Revokes this CLI's credential on the server (default: the default server), then forgets the server. A credential the server already refuses is forgotten too; when the server can't be reached, nothing is forgotten.",
			Args:   []Arg{{Name: "server", Type: "string", Desc: "server name (see `deskyard server list`)"}},
			Output: Output{Type: "text", Desc: "a confirmation line"}, Examples: []string{"deskyard logout", "deskyard logout work"},
			Where: whereServer, Exit: exitStd, run: runLogout},
		{Name: "server list", Group: "servers", Summary: "list the servers you are signed in to",
			Usage: "server list [--json]", Desc: "Lists the saved servers with your username and default environment on each; * marks the default server.",
			Flags: []Flag{flagJSON},
			Output: Output{Type: "text", Desc: "a table NAME URL USER ENVIRONMENT CREDENTIAL (credential ID only); with --json an array of {name,url,username,environment,default}",
				Fields: map[string]string{"name": "server name", "url": "server URL", "username": "account signed in", "environment": "default environment, or empty", "default": "whether it is the default server"}},
			Examples: []string{"deskyard server list"}, Where: whereLocal, Exit: exitStd, run: runServerList},
		{Name: "server default", Group: "servers", Summary: "set or show the default server",
			Usage: "server default [NAME]", Desc: "Makes NAME the server used when -e names no server; without NAME prints the current default.",
			Args:   []Arg{{Name: "name", Type: "string", Desc: "server name"}},
			Output: Output{Type: "text", Desc: "without NAME: the default's name, or nothing"}, Examples: []string{"deskyard server default work"},
			Where: whereLocal, Exit: exitStd, run: runServerDefault},
		{Name: "env use", Group: "servers", Summary: "set a server's default environment",
			Usage: "env use ENV|SERVER/ENV", Desc: "Checks that you can see the environment ENV on the default server (or SERVER), then makes it that server's default environment, used when -e isn't given.",
			Args:   []Arg{{Name: "env", Type: "string", Required: true, Desc: "environment name, or SERVER/ENV"}},
			Output: Output{Type: "text", Desc: "a confirmation line"}, Examples: []string{"deskyard env use alice", "deskyard env use work/bob"},
			Where: whereServer, Exit: exitStd, run: runEnvUse},

		// Info
		{Name: "status", Group: "info", Summary: "show the environment's name, state, image and screen",
			Usage: "status [--json]", Desc: "Shows which environment the command talks to and its state, image and screen size.",
			Flags: []Flag{flagJSON},
			Output: Output{Type: "text", Desc: `"key: value" lines; with --json an object`,
				Fields: map[string]string{"name": "environment name", "state": "running, stopped, …", "image": "image reference", "imageTitle": "image title", "imageVersion": "image version", "screen": "{width,height}", "remote": "remote used (absent locally)", "url": "environment URL (absent locally)", "local": "true when acting locally"}},
			Examples: []string{"deskyard -e alice status"}, Where: whereBoth, Exit: exitStd, run: runStatus},
		{Name: "guide", Group: "info", Summary: "short manual for environment access and control",
			Usage: "guide", Desc: "Prints a short manual: how an environment is laid out, the three ways to work (shell, browser, desktop) and when to use which.",
			Output: Output{Type: "text", Desc: "the guide"}, Examples: []string{"deskyard guide"}, Where: whereLocal, Exit: exitStd, run: runGuide},
		{Name: "describe", Group: "info", Summary: "machine-readable description of every command",
			Usage: "describe --json", Desc: "Prints every command with its arguments, flags, output and exit codes as JSON, to load as tool definitions.",
			Flags:    []Flag{{Name: "--json", Type: "boolean", Desc: "JSON output (the only format)"}},
			Output:   Output{Type: "json", Desc: "{name, version, usage, globalFlags, environment, exitCodes, commands:[{name, group, summary, usage, description, arguments, flags, output, examples, where, exitCodes}]}"},
			Examples: []string{"deskyard describe --json"}, Where: whereLocal, Exit: exitStd, run: runDescribe},
		{Name: "help", Group: "info", Summary: "help for a command",
			Usage: "help [COMMAND...]", Desc: "Prints help for a command, or the overview. Same as COMMAND --help.",
			Args:   []Arg{{Name: "command", Type: "string", Variadic: true, Desc: "command name, e.g. click or server list"}},
			Output: Output{Type: "text", Desc: "help text"}, Examples: []string{"deskyard help cp"}, Where: whereLocal, Exit: exitStd, run: runHelp},
		{Name: "forward-helper", Group: "shell", Hidden: true, Summary: "the environment side of deskyard forward",
			Usage:    "forward-helper",
			Desc:     "Serves the environment side of the forward protocol on stdin and stdout (frames with a u32 length prefix): listens on and dials 127.0.0.1 in the environment. The supervisor runs it as user for /api/environments/{name}/forward.",
			Output:   Output{Type: "bytes", Desc: "protocol frames"},
			Examples: []string{"deskyard forward-helper"},
			Where:    "inside an environment", Exit: exitStd, run: runForwardHelper},
		{Name: "activity-serve", Group: "shell", Hidden: true, Summary: "the environment's activity stream",
			Usage:    "activity-serve [--listen ADDR]",
			Desc:     "Serves the environment's activity stream: reads event datagrams on UDP ADDR, keeps the last 15 minutes, and serves them as NDJSON on GET /events over HTTP on the same ADDR. The image runs it as user; the supervisor reads it through the environment's Caddy.",
			Flags:    []Flag{{Name: "--listen", Type: "string", Value: "ADDR", Default: "127.0.0.1:6092", Desc: "address for both the UDP socket and the HTTP listener"}},
			Output:   Output{Type: "stream", Desc: "none; serves until stopped"},
			Examples: []string{"deskyard activity-serve --listen 127.0.0.1:6092"},
			Where:    "inside an environment", Exit: exitStd, run: runActivityServe},
		{Name: "version", Group: "info", Summary: "print the version",
			Usage: "version", Desc: "Prints the deskyard command's version.",
			Output: Output{Type: "text", Desc: "the version"}, Examples: []string{"deskyard version"}, Where: whereLocal, Exit: exitStd, run: runVersion},
	}
}

var globalFlags = []Flag{
	{Name: "--environment", Short: "-e", Type: "string", Value: "ENV", Desc: "the environment to use: ENV on the default server, or SERVER/ENV (see `deskyard server list`); only before the command name: after it, as in `exec -e K=V`, -e is a command's own flag"},
	{Name: "--help", Short: "-h", Type: "boolean", Desc: "help for the command"},
}

var environment = []struct {
	Name string `json:"name"`
	Desc string `json:"description"`
}{
	{"DESKYARD_ENVIRONMENT", "environment to use when -e isn't given (ENV or SERVER/ENV)"},
	{"DESKYARD_URL", "server URL, optionally with an environment (https://host/alice), to use instead of the default server; needs DESKYARD_CREDENTIAL"},
	{"DESKYARD_CREDENTIAL", "client credential (dyc_…) for DESKYARD_URL"},
	{"DESKYARD_SETTLE", "seconds screenshot and zoom wait before capturing (default 0.3)"},
	{"XDG_CONFIG_HOME", "where servers.json lives (default ~/.config)"},
}

var exitCodes = []struct {
	Code    string `json:"code"`
	Meaning string `json:"meaning"`
}{
	{"0", "ok"},
	{"1", "error (the message on stderr says what to do next)"},
	{"2", "usage error (bad command, flag or argument)"},
	{"other", "exec, shell and screen actions pass through the exit code of the command in the environment"},
}

// visibleCommands are the commands help and describe list.
func visibleCommands() []*Command {
	var out []*Command
	for _, c := range commands {
		if !c.Hidden {
			out = append(out, c)
		}
	}
	return out
}

func findCommand(name string) *Command {
	for _, c := range commands {
		if c.Name == name {
			return c
		}
	}
	return nil
}
