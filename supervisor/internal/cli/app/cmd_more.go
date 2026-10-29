package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
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
		if a.envFlag == "" && a.Inside != nil && a.Inside() {
			if src.Path == "-" || dst.Path == "-" {
				return usagef(c.cmd, "- needs a computer on the other side")
			}
			end := a.recordCommand([]string{"cp", c.args[0], c.args[1]})
			err := transfer.LocalCopy(src.Path, dst.Path)
			end(0, err)
			return err
		}
		return usagef(c.cmd, "neither side is in a computer: write the computer side as ENV:/path, e.g. deskyard cp ./file alice:/workspace/ (an empty ENV, :/path, means the chosen computer)")
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
		if cl == nil { // this computer, :/path given while inside
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

// cpTarget is the client for cp's ENV:/path side: ENV on the chosen server
// (-e SERVER/…, else the default one), or for :/path the chosen computer.
func (a *App) cpTarget(env string) (*remote.Client, error) {
	if env == "" {
		return a.target()
	}
	sel, err := a.selector()
	if err != nil {
		return nil, err
	}
	if server, _, _ := splitSelector(sel); server != "" {
		env = server + "/" + env
	}
	return a.resolve(env)
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
		Server       string `json:"server,omitempty"`
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
			Screen: screen{info.Screen.Width, info.Screen.Height}, Server: cl.Alias, URL: (&remote.Target{Base: cl.Base, Computer: info.Name}).URL()}
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
		fmt.Fprintf(tw, "server:\t%s\n", st.Server)
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
