package app

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"text/tabwriter"

	"github.com/zefhemel/deskyard/supervisor/internal/cli/remote"
)

// The env commands that manage environments on the server (env use is with
// the sign-in commands).

type envDetail struct {
	Name         string  `json:"name"`
	State        string  `json:"state"`
	Power        string  `json:"power"`
	Level        string  `json:"level"`
	Image        string  `json:"image"`
	ImageTitle   string  `json:"imageTitle"`
	ImageVersion string  `json:"imageVersion"`
	Update       *string `json:"updateAvailable"`
	Error        string  `json:"error"`
	Conflict     string  `json:"conflict"`
	Created      *string `json:"created"`
	Settings     struct {
		MemoryBytes int64   `json:"memoryBytes"`
		NanoCPUs    int64   `json:"nanoCpus"`
		Volumes     []mount `json:"volumes"`
	} `json:"settings"`
	Access  []grant  `json:"access"`
	Viewers []string `json:"viewers"`
}

func runEnvList(a *App, c *call, cl *remote.Client) error {
	var list struct {
		Environments []envDetail `json:"environments"`
		Conflicts    []struct {
			Name    string `json:"name"`
			Message string `json:"message"`
		} `json:"conflicts"`
	}
	if done, err := getJSON(a, c, cl, "/environments", &list); done || err != nil {
		return err
	}
	if len(list.Environments) == 0 {
		fmt.Fprintln(a.Stdout, "No environments you can see.")
	} else {
		var rows [][]string
		for _, e := range list.Environments {
			state := e.State
			if e.Error != "" || e.Conflict != "" {
				state += " (problem: see env show)"
			}
			rows = append(rows, []string{e.Name, state, dash(e.Level), dash(imageName(e)), dash(strings.Join(e.Viewers, ", "))})
		}
		if err := table(a.Stdout, []string{"NAME", "STATE", "ACCESS", "IMAGE", "VIEWERS"}, rows); err != nil {
			return err
		}
	}
	for _, k := range list.Conflicts {
		fmt.Fprintf(a.Stderr, "Conflict %s: %s\n", k.Name, k.Message)
	}
	return nil
}

func imageName(e envDetail) string {
	if e.ImageTitle != "" {
		return strings.TrimSpace(e.ImageTitle + " " + e.ImageVersion)
	}
	return e.Image
}

func runEnvShow(a *App, c *call, cl *remote.Client) error {
	var e envDetail
	if done, err := getJSON(a, c, cl, remote.EnvPath(cl.Environment, ""), &e); done || err != nil {
		return err
	}
	tw := tabwriter.NewWriter(a.Stdout, 0, 0, 1, ' ', 0)
	fmt.Fprintf(tw, "name:\t%s\n", e.Name)
	fmt.Fprintf(tw, "state:\t%s (power %s)\n", e.State, e.Power)
	fmt.Fprintf(tw, "image:\t%s\n", strings.TrimSpace(fmt.Sprintf("%s (%s)", imageName(e), e.Image)))
	if e.Update != nil {
		fmt.Fprintf(tw, "update:\t%s available\n", *e.Update)
	}
	fmt.Fprintf(tw, "your access:\t%s\n", dash(e.Level))
	if e.Level == "full" {
		mem, cpus := "no limit", "no limit"
		if e.Settings.MemoryBytes > 0 {
			mem = formatBytes(e.Settings.MemoryBytes)
		}
		if e.Settings.NanoCPUs > 0 {
			cpus = strconv.FormatFloat(float64(e.Settings.NanoCPUs)/1e9, 'f', -1, 64)
		}
		fmt.Fprintf(tw, "memory:\t%s\n", mem)
		fmt.Fprintf(tw, "cpus:\t%s\n", cpus)
		fmt.Fprintf(tw, "volumes:\t%s\n", formatMounts(e.Settings.Volumes))
	}
	if len(e.Access) > 0 {
		fmt.Fprintf(tw, "access:\t%s\n", formatGrants(e.Access))
	}
	fmt.Fprintf(tw, "viewers:\t%s\n", dash(strings.Join(e.Viewers, ", ")))
	fmt.Fprintf(tw, "created:\t%s\n", when(e.Created))
	if e.Error != "" {
		fmt.Fprintf(tw, "error:\t%s\n", e.Error)
	}
	if e.Conflict != "" {
		fmt.Fprintf(tw, "conflict:\t%s\n", e.Conflict)
	}
	fmt.Fprintf(tw, "url:\t%s\n", (&remote.Target{Base: cl.Base, Environment: e.Name}).URL())
	return tw.Flush()
}

// parseBytes reads --memory: bytes, or a number with K, M, G or T (powers
// of 1024).
func parseBytes(s string) (int64, bool) {
	mult := int64(1)
	num := strings.TrimSuffix(strings.TrimSuffix(strings.ToUpper(s), "B"), "I")
	if n := len(num); n > 0 {
		switch num[n-1] {
		case 'K':
			mult = 1 << 10
		case 'M':
			mult = 1 << 20
		case 'G':
			mult = 1 << 30
		case 'T':
			mult = 1 << 40
		}
		if mult > 1 {
			num = num[:n-1]
		}
	}
	f, err := strconv.ParseFloat(num, 64)
	if err != nil || f <= 0 || f*float64(mult) > math.MaxInt64/2 {
		return 0, false
	}
	return int64(f * float64(mult)), true
}

func formatBytes(n int64) string {
	for _, u := range []struct {
		size int64
		unit string
	}{{1 << 40, "T"}, {1 << 30, "G"}, {1 << 20, "M"}, {1 << 10, "K"}} {
		if n >= u.size && n%(u.size/1024) == 0 {
			return strconv.FormatFloat(float64(n)/float64(u.size), 'f', -1, 64) + u.unit
		}
	}
	return strconv.FormatInt(n, 10) + " bytes"
}

func runEnvCreate(a *App, c *call, cl *remote.Client) error {
	name := cl.Environment
	image := c.str("--image", "")
	if image == "" {
		return usagef(c.cmd, "--image is required: the image reference, e.g. ghcr.io/zefhemel/deskyard-debian-xfce:latest (see the Images page)")
	}
	body := struct {
		Name        string  `json:"name"`
		Image       string  `json:"image"`
		MemoryBytes int64   `json:"memoryBytes"`
		NanoCPUs    int64   `json:"nanoCpus"`
		Access      []grant `json:"access"`
		Volumes     []mount `json:"volumes,omitempty"`
	}{Name: name, Image: image, Access: []grant{}}
	if s := c.str("--memory", ""); s != "" {
		n, ok := parseBytes(s)
		if !ok {
			return usagef(c.cmd, "--memory wants bytes, or a size like 512M or 4G, got %q", s)
		}
		body.MemoryBytes = n
	}
	if s := c.str("--cpus", ""); s != "" {
		f, err := strconv.ParseFloat(s, 64)
		if err != nil || f <= 0 || f > 1024 {
			return usagef(c.cmd, "--cpus wants a number of CPUs like 2 or 1.5, got %q", s)
		}
		body.NanoCPUs = int64(math.Round(f * 1e9))
	}
	grants, err := parseGrants(c, "--grant", c.flags["--grant"], userGrant, "full", "observe")
	if err != nil {
		return err
	}
	body.Access = append(body.Access, grants...)
	for _, v := range c.flags["--volume"] {
		vol, mode, _ := strings.Cut(v, ":")
		if vol == "" || (mode != "" && mode != "ro") {
			return usagef(c.cmd, "--volume wants NAME or NAME:ro, got %q", v)
		}
		body.Volumes = append(body.Volumes, mount{vol, mode == "ro"})
	}
	var job struct {
		Job string `json:"job"`
	}
	if err := cl.JSON(c.ctx, http.MethodPost, "/environments", body, &job); err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(c.ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := a.followJob(ctx, cl, job.Job); err != nil {
		if errors.Is(err, context.Canceled) {
			fmt.Fprintf(a.Stderr, "Stopped following; %s is still being created on the server: see `deskyard env show %s`.\n", name, name)
		}
		return err
	}
	fmt.Fprintf(a.Stdout, "Created %s: %s\n", name, (&remote.Target{Base: cl.Base, Environment: name}).URL())
	return nil
}

// followJob prints a job's progress on stderr until it is done (nil) or
// failed (its error).
func (a *App) followJob(ctx context.Context, cl *remote.Client, id string) error {
	if id == "" {
		return fmt.Errorf("%s answered with something that isn't the Deskyard API: check the URL", cl.Base)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, cl.API("/jobs/"+url.PathEscape(id)), nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "text/event-stream")
	resp, err := cl.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return cl.ResponseError(resp)
	}
	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	last := ""
	for sc.Scan() {
		data, ok := strings.CutPrefix(sc.Text(), "data:")
		if !ok {
			continue
		}
		var u struct {
			Status   string   `json:"status"`
			Message  string   `json:"message"`
			Progress *float64 `json:"progress"`
			Error    string   `json:"error"`
		}
		if json.Unmarshal([]byte(strings.TrimSpace(data)), &u) != nil {
			continue
		}
		line := u.Message
		if u.Progress != nil && line != "" {
			// Every tenth is enough for a terminal.
			line += fmt.Sprintf(" (%d%%)", int(math.Floor(*u.Progress*10))*10)
		}
		if line != "" && line != last && u.Status == "running" {
			fmt.Fprintln(a.Stderr, line)
			last = line
		}
		switch u.Status {
		case "done":
			return nil
		case "failed":
			msg := u.Error
			if msg == "" {
				msg = u.Message
			}
			return fmt.Errorf("%s failed: %s", cl.Label(), msg)
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return fmt.Errorf("lost the progress of %s before it finished: check with `deskyard env show %s`", cl.Label(), cl.Label())
}

func runEnvRemove(a *App, c *call, cl *remote.Client) error {
	name := cl.Environment
	path := remote.EnvPath(name, "")
	if c.bool("--delete-files") {
		path += "?files=1"
	}
	if err := cl.JSONWithin(c.ctx, removalTimeout, http.MethodDelete, path, nil, nil); err != nil {
		return err
	}
	if c.bool("--delete-files") {
		fmt.Fprintf(a.Stdout, "Removed %s and its files.\n", name)
	} else {
		fmt.Fprintf(a.Stdout, "Removed %s; its files are kept (an environment created with the same name reuses them).\n", name)
	}
	return nil
}

// envPower is env start, stop or restart: it posts to the environment's
// route action and says what is happening (doing: "Starting").
func envPower(action, doing string) func(a *App, c *call) error {
	return onEnv(func(a *App, c *call, cl *remote.Client) error {
		if err := cl.JSON(c.ctx, http.MethodPost, remote.EnvPath(cl.Environment, "/"+action), nil, nil); err != nil {
			return err
		}
		fmt.Fprintf(a.Stdout, "%s %s.\n", doing, cl.Environment)
		return nil
	})
}

func runEnvAccess(a *App, c *call, cl *remote.Client) error {
	name := cl.Environment
	changes, err := parseGrants(c, "access", c.args[1:], userGrant, "full", "observe", "none")
	if err != nil {
		return err
	}
	path := remote.EnvPath(name, "")
	if len(changes) == 0 {
		var e envDetail
		if done, err := getJSON(a, c, cl, path, &e); done || err != nil {
			return err
		}
		if len(e.Access) == 0 {
			fmt.Fprintf(a.Stdout, "No regular account has access to %s (administrators always do, and only they see this list; shares are listed by `deskyard share list %s`).\n", name, name)
			return nil
		}
		var rows [][]string
		for _, g := range e.Access {
			rows = append(rows, []string{g.Username, g.Level})
		}
		return table(a.Stdout, []string{"USER", "LEVEL"}, rows)
	}
	var e envDetail
	if err := cl.JSON(c.ctx, http.MethodGet, path, nil, &e); err != nil {
		return err
	}
	// The PUT replaces every regular account's grant.
	grants := mergeGrants(e.Access, changes)
	if err := cl.JSON(c.ctx, http.MethodPut, path+"/access", map[string]any{"grants": grants}, nil); err != nil {
		return err
	}
	if len(grants) == 0 {
		fmt.Fprintf(a.Stdout, "Only administrators can use %s now.\n", name)
	} else {
		fmt.Fprintf(a.Stdout, "Access to %s: %s.\n", name, formatGrants(grants))
	}
	return nil
}
