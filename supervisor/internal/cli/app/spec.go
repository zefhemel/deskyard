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
	Name     string   `json:"name"` // "click", "remote add"
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
	whereBoth   = "inside a desktop it acts locally; elsewhere on the chosen remote (-d NAME)"
	whereLocal  = "on this machine (no remote needed)"
	whereRemote = "on the chosen remote (-d NAME); inside a desktop without a remote it acts locally"
	exitStd     = "0 ok, 1 error, 2 usage error"
	exitDesktop = "0 ok, 1 error, 2 usage error; otherwise the exit code of the action on the desktop"
	exitExec    = "the command's own exit code; 1 when it couldn't run (connection, token, stopped desktop), 2 usage error"
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
	{"remote", "Remotes"},
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
		{Name: "exec", Group: "shell", Summary: "run a command on the desktop",
			Usage: "exec [OPTIONS] -- CMD [ARG...]",
			Desc:  "Runs one command as user (in /workspace unless --cwd) and streams its stdin, stdout and stderr; exits with the command's exit code. The command is run as given, not through a shell: for pipes, globs or && use `-- bash -lc '...'`.",
			Args:  []Arg{{Name: "cmd", Type: "string", Required: true, Variadic: true, Desc: "the command and its arguments"}},
			Flags: []Flag{
				{Name: "--json", Type: "boolean", Desc: `buffer the output and print one JSON object {"stdout","stderr","exit"} instead of streaming; exit 0 when the JSON was printed`},
				{Name: "--tty", Short: "-t", Type: "boolean", Desc: "run in a terminal (for programs that need one); stdout and stderr are merged"},
				{Name: "--cwd", Type: "string", Value: "DIR", Default: "/workspace", Desc: "working directory on the desktop"},
				{Name: "--env", Short: "-e", Type: "string[]", Value: "K=V", Desc: "set an environment variable (repeatable)"},
			},
			Output: Output{Type: "stream", Desc: `the command's stdout and stderr as they arrive; with --json one object {"stdout":string,"stderr":string,"exit":int}, using "stdoutBase64"/"stderrBase64" (base64) instead when the output isn't UTF-8`,
				Fields: map[string]string{"stdout": "standard output (UTF-8)", "stderr": "standard error (UTF-8)", "exit": "exit code", "stdoutBase64": "standard output, base64, when not UTF-8", "stderrBase64": "standard error, base64, when not UTF-8"}},
			Examples: []string{"deskyard -d alice exec -- ls -la", "deskyard exec --json -- bash -lc 'cd app && npm test'", "echo hi | deskyard exec -- tee /workspace/hi.txt"},
			Where:    whereRemote, Exit: exitExec, stopAtArg: true, run: runExec},
		{Name: "shell", Group: "shell", Summary: "interactive login shell",
			Usage: "shell", Desc: "Opens an interactive bash login shell on the desktop in your terminal (raw mode, resizes follow your window). Exit with `exit` or Ctrl-D. For scripted use, prefer exec.",
			Output:   Output{Type: "stream", Desc: "the terminal session"},
			Examples: []string{"deskyard -d alice shell"},
			Where:    whereRemote, Exit: "the shell's exit code; 1 when it couldn't start", run: runShell},
		{Name: "cp", Group: "shell", Summary: "copy files to or from a desktop",
			Usage: "cp SRC DST",
			Desc: "Copies a file or directory between this machine and a desktop; write the desktop side as NAME:/path (NAME is a remote; an empty NAME, as in :/path, means the chosen remote). " +
				"A file lands at DST, or inside DST when it is an existing directory. A directory's contents land in DST, which is created (like `cp -r SRC/. DST`). " +
				"SRC - reads stdin; DST - writes to stdout (a tar stream for a directory). Files land as user; inside a desktop, two local paths are copied locally.",
			Args:     []Arg{{Name: "src", Type: "path", Required: true, Desc: "local path, -, or NAME:/path"}, {Name: "dst", Type: "path", Required: true, Desc: "local path, -, or NAME:/path"}},
			Output:   Output{Type: "none", Desc: "nothing, unless DST is - (the file's bytes or a tar stream)"},
			Examples: []string{"deskyard cp ./report.pdf alice:/workspace/", "deskyard cp ./site alice:/workspace/site", "deskyard cp alice:/workspace/out.csv ."},
			Where:    whereRemote, Exit: exitStd, run: runCp},

		{Name: "forward", Group: "shell", Summary: "map TCP ports between this machine and the desktop",
			Usage: "forward [-R] PORT[:PORT]...",
			Desc: "Maps TCP ports both ways until Ctrl-C, over the agent API (no port is opened anywhere else). Each argument is one mapping. " +
				"PORT or LOCAL:DESKTOP: this machine's 127.0.0.1:LOCAL becomes localhost:DESKTOP in the desktop (a dev server here, reachable by the desktop's browser). " +
				"-R PORT or -R DESKTOP:LOCAL (reverse; one -R before each such mapping): the desktop's localhost:DESKTOP becomes 127.0.0.1:LOCAL here. " +
				"Both ends listen on loopback only; the desktop won't listen below 1024 (plain forward only; -R just dials, so -R 80:8080 works). A port in use on either side fails that mapping, which is retried, and the others carry on. " +
				"When the connection drops (the desktop stops or restarts, the network blips) it keeps its local listeners, closes open connections and reconnects with backoff (1 s up to 30 s). " +
				"It exits only on Ctrl-C, or when the token is refused. Run it in the background. Inside a desktop it needs -d too.",
			Args:  []Arg{{Name: "port", Type: "string", Required: true, Variadic: true, Desc: "a mapping: PORT or LOCAL:DESKTOP; after -R, PORT or DESKTOP:LOCAL"}},
			Flags: []Flag{{Name: "--reverse", Short: "-R", Type: "boolean", Desc: "the next mapping is reverse: a port in the desktop becomes a port here"}},
			Output: Output{Type: "text", Desc: `one line per mapping each time it goes live: "in LOCAL DESKTOP" or "out DESKTOP 127.0.0.1:LOCAL"; on stderr one line per state change (linked, lost and retrying, port in use)`,
				Fields: map[string]string{"in": "LOCAL DESKTOP: this machine's port, reachable in the desktop at localhost:DESKTOP", "out": "DESKTOP ADDR: the desktop's port, reachable here at ADDR"}},
			Examples: []string{"deskyard -d alice forward 3000 -R 5173", "deskyard -d alice forward 8080:3000 &", "deskyard -d alice forward -R 9229:19229"},
			Where:    "on the chosen remote (-d NAME); inside a desktop too, since it maps ports between two machines. Ports are LOCAL:DESKTOP, or DESKTOP:LOCAL after -R. The desktop won't listen below 1024. \"Linked to ...\" and the out lines appear when the link is accepted, before the desktop side is known to run", Exit: "0 after Ctrl-C, 1 when the token is refused (401) or no remote is set up, 2 usage error", rawArgs: true, run: runForward},

		// Browser
		{Name: "browser connect", Group: "browser", Summary: "forward the desktop's browser to a local CDP port",
			Usage: "browser connect [--port N]",
			Desc: "Listens on 127.0.0.1:PORT and forwards the Chrome DevTools Protocol to the desktop's shared Chromium (the one on its desktop, with the human's logins), so Playwright, Puppeteer or DevTools tools connect as if the browser were local. " +
				"Prints the URL, then keeps running until Ctrl-C. Run it in the background. Inside a desktop it isn't needed: CDP is at http://127.0.0.1:9221.",
			Flags:    []Flag{{Name: "--port", Type: "integer", Value: "N", Default: "9222", Desc: "local port (0 = any free port)"}},
			Output:   Output{Type: "text", Desc: "first line: the local CDP URL (http://127.0.0.1:9222); connection log on stderr"},
			Examples: []string{"deskyard -d alice browser connect &", "# then in Python: p.chromium.connect_over_cdp(\"http://127.0.0.1:9222\")"},
			Where:    whereRemote, Exit: exitStd, run: runBrowserConnect},

		// Remotes
		{Name: "remote add", Group: "remote", Summary: "save a desktop as a remote",
			Usage: "remote add NAME URL --token dyt_…",
			Desc:  "Checks the token against the desktop's agent API and saves the remote in remotes.json (" + "$XDG_CONFIG_HOME/deskyard-desktop, default ~/.config/deskyard; mode 0600). URL is the desktop's address, like https://host/alice. Tokens come from the desktop's Agents tab. The first remote becomes the default.",
			Args: []Arg{{Name: "name", Type: "string", Required: true, Desc: "local name for the remote (letters, digits, . _ -)"},
				{Name: "url", Type: "string", Required: true, Desc: "the desktop's URL, e.g. https://host/alice"}},
			Flags:    []Flag{{Name: "--token", Type: "string", Value: "TOKEN", Desc: "agent token (dyt_…); defaults to $DESKYARD_TOKEN"}},
			Output:   Output{Type: "text", Desc: "a confirmation line"},
			Examples: []string{"deskyard remote add alice https://desktops.example.com/alice --token dyt_…"},
			Where:    whereLocal, Exit: exitStd, run: runRemoteAdd},
		{Name: "remote list", Group: "remote", Summary: "list saved remotes",
			Usage: "remote list [--json]", Desc: "Lists the saved remotes; * marks the default.",
			Flags: []Flag{flagJSON},
			Output: Output{Type: "text", Desc: "a table NAME URL TOKEN (token ID only); with --json an array of {name,url,desktop,default}",
				Fields: map[string]string{"name": "remote name", "url": "desktop URL", "desktop": "desktop name on the supervisor", "default": "whether it is the default"}},
			Examples: []string{"deskyard remote list"}, Where: whereLocal, Exit: exitStd, run: runRemoteList},
		{Name: "remote remove", Group: "remote", Summary: "forget a remote",
			Usage: "remote remove NAME", Desc: "Removes a saved remote (the token itself stays valid until someone revokes it on the Agents tab).",
			Args:   []Arg{{Name: "name", Type: "string", Required: true, Desc: "remote name"}},
			Output: noOutput, Examples: []string{"deskyard remote remove alice"}, Where: whereLocal, Exit: exitStd, run: runRemoteRemove},
		{Name: "remote default", Group: "remote", Summary: "set or show the default remote",
			Usage: "remote default [NAME]", Desc: "Makes NAME the remote used when no -d or DESKYARD_REMOTE is given; without NAME prints the current default.",
			Args:   []Arg{{Name: "name", Type: "string", Desc: "remote name"}},
			Output: Output{Type: "text", Desc: "without NAME: the default's name, or nothing"}, Examples: []string{"deskyard remote default alice"},
			Where: whereLocal, Exit: exitStd, run: runRemoteDefault},

		// Info
		{Name: "status", Group: "info", Summary: "show the desktop's name, state, image and screen",
			Usage: "status [--json]", Desc: "Shows which desktop the command talks to and its state, image and screen size.",
			Flags: []Flag{flagJSON},
			Output: Output{Type: "text", Desc: `"key: value" lines; with --json an object`,
				Fields: map[string]string{"name": "desktop name", "state": "running, stopped, …", "image": "image reference", "imageTitle": "image title", "imageVersion": "image version", "screen": "{width,height}", "remote": "remote used (absent locally)", "url": "desktop URL (absent locally)", "local": "true when acting locally"}},
			Examples: []string{"deskyard -d alice status"}, Where: whereBoth, Exit: exitStd, run: runStatus},
		{Name: "guide", Group: "info", Summary: "short manual for AI agents",
			Usage: "guide", Desc: "Prints a short manual: how a desktop is laid out, the three ways to work (shell, browser, desktop) and when to use which.",
			Output: Output{Type: "text", Desc: "the guide"}, Examples: []string{"deskyard guide"}, Where: whereLocal, Exit: exitStd, run: runGuide},
		{Name: "describe", Group: "info", Summary: "machine-readable description of every command",
			Usage: "describe --json", Desc: "Prints every command with its arguments, flags, output and exit codes as JSON, to load as tool definitions.",
			Flags:    []Flag{{Name: "--json", Type: "boolean", Desc: "JSON output (the only format)"}},
			Output:   Output{Type: "json", Desc: "{name, version, usage, globalFlags, environment, exitCodes, commands:[{name, group, summary, usage, description, arguments, flags, output, examples, where, exitCodes}]}"},
			Examples: []string{"deskyard describe --json"}, Where: whereLocal, Exit: exitStd, run: runDescribe},
		{Name: "help", Group: "info", Summary: "help for a command",
			Usage: "help [COMMAND...]", Desc: "Prints help for a command, or the overview. Same as COMMAND --help.",
			Args:   []Arg{{Name: "command", Type: "string", Variadic: true, Desc: "command name, e.g. click or remote add"}},
			Output: Output{Type: "text", Desc: "help text"}, Examples: []string{"deskyard help cp"}, Where: whereLocal, Exit: exitStd, run: runHelp},
		{Name: "forward-helper", Group: "shell", Hidden: true, Summary: "the desktop side of deskyard forward",
			Usage:    "forward-helper",
			Desc:     "Serves the desktop side of the forward protocol on stdin and stdout (frames with a u32 length prefix): listens on and dials 127.0.0.1 in the desktop. The supervisor runs it as user for /api/agent/v1/forward.",
			Output:   Output{Type: "bytes", Desc: "protocol frames"},
			Examples: []string{"deskyard forward-helper"},
			Where:    "inside a desktop", Exit: exitStd, run: runForwardHelper},
		{Name: "version", Group: "info", Summary: "print the version",
			Usage: "version", Desc: "Prints the deskyard command's version.",
			Output: Output{Type: "text", Desc: "the version"}, Examples: []string{"deskyard version"}, Where: whereLocal, Exit: exitStd, run: runVersion},
	}
}

var globalFlags = []Flag{
	{Name: "--desktop", Short: "-d", Type: "string", Value: "NAME", Desc: "use the remote NAME (see `deskyard remote list`); before the command"},
	{Name: "--help", Short: "-h", Type: "boolean", Desc: "help for the command"},
}

var environment = []struct {
	Name string `json:"name"`
	Desc string `json:"description"`
}{
	{"DESKYARD_REMOTE", "remote to use when -d isn't given"},
	{"DESKYARD_URL", "desktop URL (https://host/alice) to use without a remotes.json; needs DESKYARD_TOKEN"},
	{"DESKYARD_TOKEN", "agent token for DESKYARD_URL, and the default for `remote add --token`"},
	{"DESKYARD_SETTLE", "seconds screenshot and zoom wait before capturing (default 0.3)"},
	{"XDG_CONFIG_HOME", "where remotes.json lives (default ~/.config)"},
}

var exitCodes = []struct {
	Code    string `json:"code"`
	Meaning string `json:"meaning"`
}{
	{"0", "ok"},
	{"1", "error (the message on stderr says what to do next)"},
	{"2", "usage error (bad command, flag or argument)"},
	{"other", "exec, shell and screen actions pass through the exit code of the command on the desktop"},
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
