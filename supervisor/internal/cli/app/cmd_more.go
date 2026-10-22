package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"text/tabwriter"

	"github.com/zefhemel/deskyard/supervisor/internal/cli/cdp"
	"github.com/zefhemel/deskyard/supervisor/internal/cli/execws"
	"github.com/zefhemel/deskyard/supervisor/internal/cli/remote"
	"github.com/zefhemel/deskyard/supervisor/internal/cli/transfer"
)

// ---- cp

func runCp(a *App, c *call) error {
	src, dst := transfer.ParseEndpoint(c.args[0]), transfer.ParseEndpoint(c.args[1])
	switch {
	case src.IsRemote && dst.IsRemote:
		return usagef(c.cmd, "both sides are on computers; copy to this machine first, then on")
	case !src.IsRemote && !dst.IsRemote:
		if a.remoteFlag == "" && a.Inside != nil && a.Inside() {
			if src.Path == "-" || dst.Path == "-" {
				return usagef(c.cmd, "- needs a computer on the other side")
			}
			return transfer.LocalCopy(src.Path, dst.Path)
		}
		return usagef(c.cmd, "neither side is on a computer: write the computer side as NAME:/path, e.g. computer cp ./file alice:/workspace/ (an empty NAME, :/path, means the chosen remote)")
	}
	rp := src
	if dst.IsRemote {
		rp = dst
	}
	cl, err := a.cpTarget(rp.Remote)
	if err != nil {
		return err
	}
	run := func(ctx context.Context, req execws.Request, sio execws.IO) (int, error) {
		if cl == nil { // local computer, NAME: given while inside
			code, err := a.localExec(req, sio.Stdin, orDiscard(sio.Stdout), orDiscard(sio.Stderr))
			return code, err
		}
		return execws.Run(ctx, cl, req, sio)
	}
	if dst.IsRemote {
		return transfer.Upload(c.ctx, run, src.Path, dst.Path, a.Stdin)
	}
	return transfer.Download(c.ctx, run, src.Path, dst.Path, a.Stdout)
}

func (a *App) cpTarget(name string) (*remote.Client, error) {
	if name == "" {
		cl, err := a.target()
		if err == nil && cl == nil {
			return nil, nil
		}
		return cl, err
	}
	return a.namedRemote(name)
}

func orDiscard(w io.Writer) io.Writer {
	if w == nil {
		return io.Discard
	}
	return w
}

// ---- browser connect

func runBrowserConnect(a *App, c *call) error {
	port, err := strconv.Atoi(c.str("--port", "9222"))
	if err != nil || port < 0 || port > 65535 {
		return usagef(c.cmd, "--port wants a port number, got %q", c.str("--port", ""))
	}
	cl, err := a.target()
	if err != nil {
		return err
	}
	if cl == nil {
		fmt.Fprintln(a.Stdout, "http://127.0.0.1:9221")
		fmt.Fprintln(a.Stderr, "You are inside the computer: the shared Chromium's CDP is already at http://127.0.0.1:9221; connect to it directly (connect_over_cdp(\"http://127.0.0.1:9221\")).")
		return nil
	}
	ctx, stop := signal.NotifyContext(c.ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()
	fmt.Fprintf(a.Stderr, "Connecting to %s's browser (the first connection may take a few seconds while Chromium starts)...\n", cl.Label())
	ver, err := cdp.Probe(ctx, cl)
	if err != nil {
		return err
	}
	logf := func(format string, args ...any) { fmt.Fprintf(a.Stderr, format+"\n", args...) }
	err = cdp.Serve(ctx, cl, port, func(addr string) {
		u := "http://" + addr
		fmt.Fprintln(a.Stdout, u)
		browser, _ := ver["Browser"].(string)
		fmt.Fprintf(a.Stderr, "Forwarding %s to %s's shared browser (%s).\n", u, cl.Label(), browser)
		fmt.Fprintf(a.Stderr, "Playwright: chromium.connect_over_cdp(%q), then browser.contexts[0].new_page().\n", u)
		fmt.Fprintln(a.Stderr, "Press Ctrl-C to stop.")
	}, logf)
	if err != nil {
		return err
	}
	return nil
}

// ---- remotes

func runRemoteAdd(a *App, c *call) error {
	name, rawURL := c.args[0], c.args[1]
	if !remote.ValidName(name) {
		return usagef(c.cmd, "%q isn't a usable remote name: use letters, digits, '.', '_' and '-'", name)
	}
	tok := c.str("--token", a.Getenv("COMPUTER_TOKEN"))
	if tok == "" {
		return usagef(c.cmd, "no token: pass --token dyt_… (create one on the computer's Agents tab)")
	}
	if !strings.HasPrefix(tok, "dyt_") {
		return usagef(c.cmd, "that doesn't look like an agent token (they start with dyt_): copy it from the computer's Agents tab")
	}
	t, err := remote.ParseURL(rawURL)
	if err != nil {
		return usagef(c.cmd, "%v", err)
	}
	cl := &remote.Client{Alias: name, Base: t.Base, Computer: t.Computer, Token: tok, HTTP: a.HTTP}
	info, err := cl.Info(c.ctx)
	stopped := err
	switch {
	case err == nil:
		if t.Computer != "" && info.Name != "" && info.Name != t.Computer {
			return fmt.Errorf("this token is for the computer %q, not %q: use https://…/%s, or a token for %s", info.Name, t.Computer, info.Name, t.Computer)
		}
		if info.Name != "" {
			t.Computer = info.Name
		}
	case remote.IsStatus(err, http.StatusConflict):
		// The token works; the computer is just not running.
	default:
		return err
	}
	cfg, path, err := a.loadConfig()
	if err != nil {
		return err
	}
	_, replaced := cfg.Remotes[name]
	cfg.Add(&remote.Remote{Name: name, URL: t.URL(), Base: t.Base, Computer: t.Computer, Token: tok})
	if err := cfg.Save(path); err != nil {
		return fmt.Errorf("saving %s: %v", path, err)
	}
	verb := "Added"
	if replaced {
		verb = "Updated"
	}
	switch {
	case stopped != nil:
		fmt.Fprintf(a.Stdout, "%s remote %s (%s). Note: %v\n", verb, name, t.URL(), stopped)
	default:
		fmt.Fprintf(a.Stdout, "%s remote %s (%s: %s, %s %s).\n", verb, name, t.URL(), info.State, info.ImageTitle, info.ImageVersion)
	}
	if cfg.Default == name {
		fmt.Fprintf(a.Stdout, "It is the default remote; try `computer status`.\n")
	} else {
		fmt.Fprintf(a.Stdout, "Use it with `computer -c %s …`, or make it the default with `computer remote default %s`.\n", name, name)
	}
	return nil
}

func runRemoteList(a *App, c *call) error {
	cfg, _, err := a.loadConfig()
	if err != nil {
		return err
	}
	if c.bool("--json") {
		type row struct {
			Name     string `json:"name"`
			URL      string `json:"url"`
			Computer string `json:"computer"`
			Default  bool   `json:"default"`
		}
		rows := []row{}
		for _, r := range cfg.Sorted() {
			rows = append(rows, row{r.Name, r.URL, r.Computer, r.Name == cfg.Default})
		}
		enc := json.NewEncoder(a.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(rows)
	}
	if len(cfg.Remotes) == 0 {
		fmt.Fprintln(a.Stdout, "No remotes. Get a token on a computer's Agents tab, then: computer remote add NAME https://host/NAME --token dyt_…")
		return nil
	}
	tw := tabwriter.NewWriter(a.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "  NAME\tURL\tTOKEN")
	for _, r := range cfg.Sorted() {
		mark := " "
		if r.Name == cfg.Default {
			mark = "*"
		}
		fmt.Fprintf(tw, "%s %s\t%s\t%s\n", mark, r.Name, r.URL, remote.MaskToken(r.Token))
	}
	return tw.Flush()
}

func runRemoteRemove(a *App, c *call) error {
	cfg, path, err := a.loadConfig()
	if err != nil {
		return err
	}
	if !cfg.Remove(c.args[0]) {
		return fmt.Errorf("no remote named %q: run `computer remote list`", c.args[0])
	}
	return cfg.Save(path)
}

func runRemoteDefault(a *App, c *call) error {
	cfg, path, err := a.loadConfig()
	if err != nil {
		return err
	}
	if len(c.args) == 0 {
		if cfg.Default != "" {
			fmt.Fprintln(a.Stdout, cfg.Default)
		}
		return nil
	}
	name := c.args[0]
	if cfg.Remotes[name] == nil {
		return fmt.Errorf("no remote named %q: run `computer remote list`", name)
	}
	cfg.Default = name
	return cfg.Save(path)
}

// ---- status

func runStatus(a *App, c *call) error {
	cl, err := a.target()
	if err != nil {
		return err
	}
	type screen struct {
		Width  int `json:"width"`
		Height int `json:"height"`
	}
	type status struct {
		Name         string `json:"name"`
		State        string `json:"state"`
		Image        string `json:"image,omitempty"`
		ImageTitle   string `json:"imageTitle,omitempty"`
		ImageVersion string `json:"imageVersion,omitempty"`
		Screen       screen `json:"screen"`
		Remote       string `json:"remote,omitempty"`
		URL          string `json:"url,omitempty"`
		Local        bool   `json:"local,omitempty"`
	}
	var st status
	if cl == nil {
		host, _ := os.Hostname()
		st = status{Name: host, State: "running", Local: true}
		if out, err := localOutput(a, "xdotool", "getdisplaygeometry"); err == nil {
			f := strings.Fields(out)
			if len(f) == 2 {
				st.Screen.Width, _ = strconv.Atoi(f[0])
				st.Screen.Height, _ = strconv.Atoi(f[1])
			}
		}
	} else {
		info, err := cl.Info(c.ctx)
		if err != nil {
			return err
		}
		st = status{Name: info.Name, State: info.State, Image: info.Image, ImageTitle: info.ImageTitle, ImageVersion: info.ImageVersion,
			Screen: screen{info.Screen.Width, info.Screen.Height}, Remote: cl.Alias, URL: (&remote.Target{Base: cl.Base, Computer: info.Name}).URL()}
	}
	if c.bool("--json") {
		enc := json.NewEncoder(a.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(st)
	}
	tw := tabwriter.NewWriter(a.Stdout, 0, 0, 1, ' ', 0)
	fmt.Fprintf(tw, "name:\t%s\n", st.Name)
	fmt.Fprintf(tw, "state:\t%s\n", st.State)
	if st.Image != "" {
		fmt.Fprintf(tw, "image:\t%s\n", strings.TrimSpace(fmt.Sprintf("%s %s (%s)", st.ImageTitle, st.ImageVersion, st.Image)))
	}
	if st.Screen.Width > 0 {
		fmt.Fprintf(tw, "screen:\t%dx%d\n", st.Screen.Width, st.Screen.Height)
	}
	if st.Local {
		fmt.Fprintf(tw, "where:\tlocal (inside this computer)\n")
	} else {
		fmt.Fprintf(tw, "remote:\t%s\n", st.Remote)
		fmt.Fprintf(tw, "url:\t%s\n", st.URL)
	}
	return tw.Flush()
}

func localOutput(a *App, name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	cmd.Env = os.Environ()
	if a.Getenv("DISPLAY") == "" {
		cmd.Env = append(cmd.Env, "DISPLAY=:1")
	}
	out, err := cmd.Output()
	if err != nil {
		return "", errors.New(strings.TrimSpace(string(out)))
	}
	return string(out), nil
}
