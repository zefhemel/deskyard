package app

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"text/tabwriter"
	"time"

	"github.com/zefhemel/deskyard/supervisor/internal/cli/remote"
)

// The administrator commands: environments, volumes, accounts, shares and
// signed-in devices, all through the server's API (supervisor/API.md). The
// server decides who may do what; its refusals are printed as they come.

// timeNow is the clock --expires durations count from (tests fix it).
var timeNow = time.Now

// apiClient is the client for the server the command talks to: --server,
// else the server -e SERVER/ENV or DESKYARD_ENVIRONMENT names, else the
// default one. env names the environment the command is about, for messages.
func (a *App) apiClient(c *call, env string) (*remote.Client, error) {
	name := c.str("--server", "")
	if name == "" {
		sel, err := a.selector()
		if err != nil {
			return nil, err
		}
		name, _, _ = splitSelector(sel)
	}
	srv, err := a.server(name)
	if err != nil {
		return nil, err
	}
	if srv == nil {
		return nil, a.noServer()
	}
	cl := remote.FromServer(srv, env)
	cl.HTTP = a.HTTP
	return cl, nil
}

// printJSON prints an API answer as it came, indented.
func printJSON(w io.Writer, raw json.RawMessage) error {
	var b bytes.Buffer
	if err := json.Indent(&b, raw, "", "  "); err != nil {
		return err
	}
	b.WriteByte('\n')
	_, err := b.WriteTo(w)
	return err
}

// getJSON fetches path; with --json it prints the answer and reports done.
func getJSON(a *App, c *call, cl *remote.Client, path string, out any) (done bool, err error) {
	var raw json.RawMessage
	if err := cl.JSON(c.ctx, http.MethodGet, path, nil, &raw); err != nil {
		return false, err
	}
	if c.bool("--json") {
		return true, printJSON(a.Stdout, raw)
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return false, fmt.Errorf("%s answered with something that isn't the Deskyard API (%v): check the URL", cl.Base, err)
	}
	return false, nil
}

func dash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

// when formats an API time for tables, "-" for none.
func when(s *string) string {
	if s == nil || *s == "" {
		return "-"
	}
	t, err := time.Parse(time.RFC3339, *s)
	if err != nil {
		return *s
	}
	return t.Local().Format("2006-01-02 15:04")
}

// parseWhen reads --expires: a duration from now (90m, 2h, 7d, 2w) or an
// RFC 3339 time.
func parseWhen(c *call, s string) (time.Time, error) {
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t.UTC(), nil
	}
	var d time.Duration
	var err error
	if n, unit := strings.TrimRight(s, "dw"), strings.TrimLeft(s, "0123456789"); n != s && (unit == "d" || unit == "w") {
		var k int
		if k, err = strconv.Atoi(n); err == nil {
			d = time.Duration(k) * 24 * time.Hour
			if unit == "w" {
				d *= 7
			}
		}
	} else {
		d, err = time.ParseDuration(s)
	}
	if err != nil || d <= 0 {
		return time.Time{}, usagef(c.cmd, "--expires wants a time from now (2h, 7d, 2w) or an RFC 3339 time (2026-12-01T18:00:00Z), got %q", s)
	}
	return timeNow().Add(d).UTC().Truncate(time.Second), nil
}

// levelOf splits NAME[=LEVEL]; a bare NAME is full. allowed are the levels
// accepted after =.
func levelOf(c *call, what, s string, allowed ...string) (name, level string, err error) {
	name, level, ok := strings.Cut(s, "=")
	if !ok {
		level = "full"
	}
	if name == "" {
		return "", "", usagef(c.cmd, "%s wants NAME or NAME=LEVEL, got %q", what, s)
	}
	for _, l := range allowed {
		if level == l {
			return name, level, nil
		}
	}
	return "", "", usagef(c.cmd, "%s %q: the level is one of %s", what, s, strings.Join(allowed, ", "))
}

// ---- environments

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
		MemoryBytes int64 `json:"memoryBytes"`
		NanoCPUs    int64 `json:"nanoCpus"`
		Volumes     []struct {
			Name     string `json:"name"`
			ReadOnly bool   `json:"readOnly"`
		} `json:"volumes"`
	} `json:"settings"`
	Access  []accessGrant `json:"access"`
	Viewers []string      `json:"viewers"`
}

type accessGrant struct {
	Username string `json:"username"`
	Level    string `json:"level"`
}

func runEnvList(a *App, c *call) error {
	cl, err := a.apiClient(c, "")
	if err != nil {
		return err
	}
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
		tw := tabwriter.NewWriter(a.Stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(tw, "NAME\tSTATE\tACCESS\tIMAGE\tVIEWERS")
		for _, e := range list.Environments {
			state := e.State
			if e.Error != "" || e.Conflict != "" {
				state += " (problem: see env show)"
			}
			fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", e.Name, state, dash(e.Level), dash(imageName(e)), dash(strings.Join(e.Viewers, ", ")))
		}
		if err := tw.Flush(); err != nil {
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

func runEnvShow(a *App, c *call) error {
	name := c.args[0]
	cl, err := a.apiClient(c, name)
	if err != nil {
		return err
	}
	var e envDetail
	if done, err := getJSON(a, c, cl, "/environments/"+url.PathEscape(name), &e); done || err != nil {
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
		var vols []string
		for _, v := range e.Settings.Volumes {
			if v.ReadOnly {
				vols = append(vols, v.Name+" (read-only)")
			} else {
				vols = append(vols, v.Name)
			}
		}
		fmt.Fprintf(tw, "volumes:\t%s\n", dash(strings.Join(vols, ", ")))
	}
	if len(e.Access) > 0 {
		fmt.Fprintf(tw, "access:\t%s\n", formatAccess(e.Access))
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

func formatAccess(grants []accessGrant) string {
	var out []string
	for _, g := range grants {
		if g.Level == "full" {
			out = append(out, g.Username)
		} else {
			out = append(out, g.Username+" ("+g.Level+")")
		}
	}
	return strings.Join(out, ", ")
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

func runEnvCreate(a *App, c *call) error {
	name := c.args[0]
	image := c.str("--image", "")
	if image == "" {
		return usagef(c.cmd, "--image is required: the image reference, e.g. ghcr.io/zefhemel/deskyard-debian-xfce:latest (see the Images page)")
	}
	type volumeGrant struct {
		Name     string `json:"name"`
		ReadOnly bool   `json:"readOnly"`
	}
	body := struct {
		Name        string        `json:"name"`
		Image       string        `json:"image"`
		MemoryBytes int64         `json:"memoryBytes"`
		NanoCPUs    int64         `json:"nanoCpus"`
		Access      []accessGrant `json:"access"`
		Volumes     []volumeGrant `json:"volumes,omitempty"`
	}{Name: name, Image: image, Access: []accessGrant{}}
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
	for _, g := range c.flags["--grant"] {
		user, level, err := levelOf(c, "--grant", g, "full", "observe")
		if err != nil {
			return err
		}
		body.Access = append(body.Access, accessGrant{user, level})
	}
	for _, v := range c.flags["--volume"] {
		vol, mode, _ := strings.Cut(v, ":")
		if vol == "" || (mode != "" && mode != "ro") {
			return usagef(c.cmd, "--volume wants NAME or NAME:ro, got %q", v)
		}
		body.Volumes = append(body.Volumes, volumeGrant{vol, mode == "ro"})
	}
	cl, err := a.apiClient(c, name)
	if err != nil {
		return err
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

func runEnvRemove(a *App, c *call) error {
	name := c.args[0]
	cl, err := a.apiClient(c, name)
	if err != nil {
		return err
	}
	path := "/environments/" + url.PathEscape(name)
	if c.bool("--delete-files") {
		path += "?files=1"
	}
	if err := cl.JSON(c.ctx, http.MethodDelete, path, nil, nil); err != nil {
		return err
	}
	if c.bool("--delete-files") {
		fmt.Fprintf(a.Stdout, "Removed %s and its files.\n", name)
	} else {
		fmt.Fprintf(a.Stdout, "Removed %s; its files are kept (an environment created with the same name reuses them).\n", name)
	}
	return nil
}

// runEnvPower is env start, stop and restart.
func runEnvPower(a *App, c *call) error {
	name := c.args[0]
	action := strings.TrimPrefix(c.cmd.Name, "env ")
	cl, err := a.apiClient(c, name)
	if err != nil {
		return err
	}
	if err := cl.JSON(c.ctx, http.MethodPost, "/environments/"+url.PathEscape(name)+"/"+action, nil, nil); err != nil {
		return err
	}
	done := map[string]string{"start": "Starting", "stop": "Stopping", "restart": "Restarting"}[action]
	fmt.Fprintf(a.Stdout, "%s %s.\n", done, name)
	return nil
}

func runEnvAccess(a *App, c *call) error {
	name := c.args[0]
	changes := map[string]string{}
	var order []string
	for _, s := range c.args[1:] {
		user, level, ok := strings.Cut(s, "=")
		if !ok || user == "" || (level != "full" && level != "observe" && level != "none") {
			return usagef(c.cmd, "%q: write USER=full, USER=observe or USER=none", s)
		}
		if _, dup := changes[user]; dup {
			return usagef(c.cmd, "%s is listed twice", user)
		}
		changes[user] = level
		order = append(order, user)
	}
	cl, err := a.apiClient(c, name)
	if err != nil {
		return err
	}
	path := "/environments/" + url.PathEscape(name)
	if len(changes) == 0 {
		var e envDetail
		if done, err := getJSON(a, c, cl, path, &e); done || err != nil {
			return err
		}
		if len(e.Access) == 0 {
			fmt.Fprintf(a.Stdout, "No regular account has access to %s (administrators always do, and only they see this list; shares are listed by `deskyard share list %s`).\n", name, name)
			return nil
		}
		tw := tabwriter.NewWriter(a.Stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(tw, "USER\tLEVEL")
		for _, g := range e.Access {
			fmt.Fprintf(tw, "%s\t%s\n", g.Username, g.Level)
		}
		return tw.Flush()
	}
	var e envDetail
	if err := cl.JSON(c.ctx, http.MethodGet, path, nil, &e); err != nil {
		return err
	}
	// The PUT replaces every regular account's grant: keep the ones not
	// named, then apply the changes in the order given.
	grants := []accessGrant{}
	for _, g := range e.Access {
		if _, named := changes[g.Username]; !named {
			grants = append(grants, g)
		}
	}
	for _, user := range order {
		if l := changes[user]; l != "none" {
			grants = append(grants, accessGrant{user, l})
		}
	}
	if err := cl.JSON(c.ctx, http.MethodPut, path+"/access", map[string]any{"grants": grants}, nil); err != nil {
		return err
	}
	if len(grants) == 0 {
		fmt.Fprintf(a.Stdout, "Only administrators can use %s now.\n", name)
	} else {
		fmt.Fprintf(a.Stdout, "Access to %s: %s.\n", name, formatAccess(grants))
	}
	return nil
}

// ---- volumes

func runVolumeList(a *App, c *call) error {
	cl, err := a.apiClient(c, "")
	if err != nil {
		return err
	}
	var list struct {
		Volumes []struct {
			Name         string  `json:"name"`
			Folder       string  `json:"folder"`
			Created      *string `json:"created"`
			Environments []struct {
				Name     string `json:"name"`
				ReadOnly bool   `json:"readOnly"`
			} `json:"environments"`
		} `json:"volumes"`
	}
	if done, err := getJSON(a, c, cl, "/volumes", &list); done || err != nil {
		return err
	}
	if len(list.Volumes) == 0 {
		fmt.Fprintln(a.Stdout, "No volumes: create one with `deskyard volume create NAME`.")
		return nil
	}
	tw := tabwriter.NewWriter(a.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "NAME\tENVIRONMENTS\tFOLDER\tCREATED")
	for _, v := range list.Volumes {
		var envs []string
		for _, e := range v.Environments {
			if e.ReadOnly {
				envs = append(envs, e.Name+" (read-only)")
			} else {
				envs = append(envs, e.Name)
			}
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", v.Name, dash(strings.Join(envs, ", ")), v.Folder, when(v.Created))
	}
	return tw.Flush()
}

func runVolumeCreate(a *App, c *call) error {
	cl, err := a.apiClient(c, "")
	if err != nil {
		return err
	}
	var ans struct {
		Volume struct {
			Name   string `json:"name"`
			Folder string `json:"folder"`
		} `json:"volume"`
		Adopted bool `json:"adopted"`
	}
	if err := cl.JSON(c.ctx, http.MethodPost, "/volumes", map[string]string{"name": c.args[0]}, &ans); err != nil {
		return err
	}
	if ans.Adopted {
		fmt.Fprintf(a.Stdout, "Created the volume %s, keeping the existing folder %s in the data folder.\n", ans.Volume.Name, ans.Volume.Folder)
	} else {
		fmt.Fprintf(a.Stdout, "Created the volume %s (%s in the data folder). Grant it with `deskyard env create … --volume %s`.\n", ans.Volume.Name, ans.Volume.Folder, ans.Volume.Name)
	}
	return nil
}

func runVolumeRemove(a *App, c *call) error {
	cl, err := a.apiClient(c, "")
	if err != nil {
		return err
	}
	del := c.bool("--delete-files")
	if err := cl.JSON(c.ctx, http.MethodDelete, "/volumes/"+url.PathEscape(c.args[0])+"?deleteFiles="+strconv.FormatBool(del), nil, nil); err != nil {
		return err
	}
	if del {
		fmt.Fprintf(a.Stdout, "Removed the volume %s and its files.\n", c.args[0])
	} else {
		fmt.Fprintf(a.Stdout, "Removed the volume %s; its folder is kept.\n", c.args[0])
	}
	return nil
}

// ---- accounts

type grant struct {
	Environment string `json:"environment"`
	Level       string `json:"level"`
}

type account struct {
	Username    string  `json:"username"`
	Kind        string  `json:"kind"`
	Admin       bool    `json:"admin"`
	Description string  `json:"description"`
	Grants      []grant `json:"grants"`
	SharedBy    *struct {
		Username string `json:"username"`
	} `json:"sharedBy"`
	Expires     *string `json:"expires"`
	LinkPending bool    `json:"linkPending"`
	LastLogin   *string `json:"lastLogin"`
}

func formatGrants(gs []grant) string {
	var out []string
	for _, g := range gs {
		if g.Level == "full" {
			out = append(out, g.Environment)
		} else {
			out = append(out, g.Environment+" ("+g.Level+")")
		}
	}
	return strings.Join(out, ", ")
}

func runAccountList(a *App, c *call) error {
	cl, err := a.apiClient(c, "")
	if err != nil {
		return err
	}
	var list []account
	if done, err := getJSON(a, c, cl, "/accounts", &list); done || err != nil {
		return err
	}
	// A regular account is given the usernames only (they have no kind).
	if len(list) > 0 && list[0].Kind == "" {
		fmt.Fprintln(a.Stdout, "USER")
		for _, ac := range list {
			fmt.Fprintln(a.Stdout, ac.Username)
		}
		fmt.Fprintln(a.Stderr, "Only an admin sees the accounts' access; these are their usernames.")
		return nil
	}
	tw := tabwriter.NewWriter(a.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "USER\tKIND\tACCESS\tEXPIRES\tLAST SIGN-IN\tNOTE")
	for _, ac := range list {
		access := formatGrants(ac.Grants)
		if ac.Admin {
			access = "admin (everything)"
		}
		var note []string
		if ac.SharedBy != nil {
			note = append(note, "shared by "+ac.SharedBy.Username)
		}
		if ac.LinkPending {
			note = append(note, "unused link")
		}
		if ac.Description != "" {
			note = append(note, ac.Description)
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n", ac.Username, dash(ac.Kind), dash(access), when(ac.Expires), when(ac.LastLogin), dash(strings.Join(note, "; ")))
	}
	return tw.Flush()
}

// readPassword reads --password-stdin's password: all of stdin, without
// the final line break.
func readPassword(a *App, c *call) (string, error) {
	data, err := io.ReadAll(io.LimitReader(a.Stdin, 4096))
	if err != nil {
		return "", err
	}
	pw := strings.TrimSuffix(strings.TrimSuffix(string(data), "\n"), "\r")
	if pw == "" {
		return "", usagef(c.cmd, "--password-stdin read an empty password: pipe it in, e.g. printf '%%s' \"$PW\" | deskyard %s …", c.cmd.Name)
	}
	return pw, nil
}

func runAccountCreate(a *App, c *call) error {
	user := c.args[0]
	body := struct {
		Username    string  `json:"username"`
		Password    string  `json:"password,omitempty"`
		Admin       bool    `json:"admin,omitempty"`
		Description *string `json:"description,omitempty"`
		Grants      []grant `json:"grants,omitempty"`
		Expires     string  `json:"expires,omitempty"`
	}{Username: user, Admin: c.bool("--admin")}
	if c.bool("--password-stdin") {
		pw, err := readPassword(a, c)
		if err != nil {
			return err
		}
		body.Password = pw
	}
	if v := c.flags["--description"]; len(v) > 0 {
		body.Description = &v[len(v)-1]
	}
	for _, g := range c.flags["--grant"] {
		env, level, err := levelOf(c, "--grant", g, "full", "observe")
		if err != nil {
			return err
		}
		body.Grants = append(body.Grants, grant{env, level})
	}
	if s := c.str("--expires", ""); s != "" {
		t, err := parseWhen(c, s)
		if err != nil {
			return err
		}
		body.Expires = t.Format(time.RFC3339)
	}
	cl, err := a.apiClient(c, "")
	if err != nil {
		return err
	}
	if err := cl.JSON(c.ctx, http.MethodPost, "/accounts", body, nil); err != nil {
		return err
	}
	if body.Password != "" {
		fmt.Fprintf(a.Stdout, "Created the account %s; it signs in with its password.\n", user)
		return nil
	}
	return a.printLink(c, cl, "/accounts/"+url.PathEscape(user)+"/link", user,
		fmt.Sprintf("Created the token account %s. Its login link", user))
}

// printLink asks for a login link at path and prints it alone on stdout,
// what is said about it on stderr.
func (a *App) printLink(c *call, cl *remote.Client, path, user, what string) error {
	var ans struct {
		Link string `json:"link"`
	}
	if err := cl.JSON(c.ctx, http.MethodPost, path, nil, &ans); err != nil {
		return err
	}
	fmt.Fprintf(a.Stderr, "%s works once: anyone with it can sign in as %s until it is used. A new one replaces it.\n", what, user)
	fmt.Fprintln(a.Stdout, ans.Link)
	return nil
}

func runAccountUpdate(a *App, c *call) error {
	user := c.args[0]
	body := map[string]any{}
	if c.bool("--password-stdin") {
		pw, err := readPassword(a, c)
		if err != nil {
			return err
		}
		body["password"] = pw
	}
	switch {
	case c.bool("--admin") && c.bool("--no-admin"):
		return usagef(c.cmd, "--admin and --no-admin contradict each other")
	case c.bool("--admin"):
		body["admin"] = true
	case c.bool("--no-admin"):
		body["admin"] = false
	}
	if v := c.flags["--description"]; len(v) > 0 {
		body["description"] = v[len(v)-1]
	}
	switch s := c.str("--expires", ""); s {
	case "":
	case "never":
		body["expires"] = nil
	default:
		t, err := parseWhen(c, s)
		if err != nil {
			return err
		}
		body["expires"] = t.Format(time.RFC3339)
	}
	changes := map[string]string{}
	var order []string
	for _, g := range c.flags["--grant"] {
		env, level, err := levelOf(c, "--grant", g, "full", "observe", "none")
		if err != nil {
			return err
		}
		if _, dup := changes[env]; !dup {
			order = append(order, env)
		}
		changes[env] = level
	}
	if len(body) == 0 && len(changes) == 0 {
		return usagef(c.cmd, "nothing to change: give --grant, --admin, --no-admin, --description, --expires or --password-stdin")
	}
	cl, err := a.apiClient(c, "")
	if err != nil {
		return err
	}
	if len(changes) > 0 {
		// grants replaces the whole list: keep the environments not named.
		var list []account
		if err := cl.JSON(c.ctx, http.MethodGet, "/accounts", nil, &list); err != nil {
			return err
		}
		var cur *account
		for i := range list {
			if list[i].Username == user {
				cur = &list[i]
			}
		}
		if cur == nil {
			return fmt.Errorf("no such account %q: see `deskyard account list`", user)
		}
		grants := []grant{}
		for _, g := range cur.Grants {
			if _, named := changes[g.Environment]; !named {
				grants = append(grants, g)
			}
		}
		for _, env := range order {
			if l := changes[env]; l != "none" {
				grants = append(grants, grant{env, l})
			}
		}
		body["grants"] = grants
	}
	var ac account
	if err := cl.JSON(c.ctx, http.MethodPatch, "/accounts/"+url.PathEscape(user), body, &ac); err != nil {
		return err
	}
	access := formatGrants(ac.Grants)
	if ac.Admin {
		access = "admin (everything)"
	}
	fmt.Fprintf(a.Stdout, "Updated %s: %s; expires %s.\n", user, dash(access), when(ac.Expires))
	return nil
}

func runAccountRemove(a *App, c *call) error {
	cl, err := a.apiClient(c, "")
	if err != nil {
		return err
	}
	if err := cl.JSON(c.ctx, http.MethodDelete, "/accounts/"+url.PathEscape(c.args[0]), nil, nil); err != nil {
		return err
	}
	fmt.Fprintf(a.Stdout, "Removed the account %s, with its login link, devices and the shares it made.\n", c.args[0])
	return nil
}

func runAccountLink(a *App, c *call) error {
	cl, err := a.apiClient(c, "")
	if err != nil {
		return err
	}
	user := c.args[0]
	return a.printLink(c, cl, "/accounts/"+url.PathEscape(user)+"/link", user, "The login link of "+user)
}

// ---- sharing

func runShareList(a *App, c *call) error {
	env := c.args[0]
	cl, err := a.apiClient(c, env)
	if err != nil {
		return err
	}
	var list []struct {
		Username string `json:"username"`
		Level    string `json:"level"`
		SharedBy struct {
			Username string `json:"username"`
		} `json:"sharedBy"`
		Expires     *string `json:"expires"`
		LastLogin   *string `json:"lastLogin"`
		LinkPending bool    `json:"linkPending"`
		Connected   bool    `json:"connected"`
	}
	if done, err := getJSON(a, c, cl, "/environments/"+url.PathEscape(env)+"/shares", &list); done || err != nil {
		return err
	}
	if len(list) == 0 {
		fmt.Fprintf(a.Stdout, "%s isn't shared: share it with `deskyard share create %s NAME`.\n", env, env)
		return nil
	}
	tw := tabwriter.NewWriter(a.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "NAME\tLEVEL\tSHARED BY\tEXPIRES\tLAST SIGN-IN\tNOTE")
	for _, s := range list {
		var note []string
		if s.Connected {
			note = append(note, "connected")
		}
		if s.LinkPending {
			note = append(note, "unused link")
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n", s.Username, s.Level, s.SharedBy.Username, when(s.Expires), when(s.LastLogin), dash(strings.Join(note, ", ")))
	}
	return tw.Flush()
}

func runShareCreate(a *App, c *call) error {
	env, user := c.args[0], c.args[1]
	body := struct {
		Username string `json:"username"`
		Level    string `json:"level"`
		Expires  string `json:"expires,omitempty"`
	}{Username: user, Level: "full"}
	if c.bool("--observe") {
		body.Level = "observe"
	}
	if s := c.str("--expires", ""); s != "" {
		t, err := parseWhen(c, s)
		if err != nil {
			return err
		}
		body.Expires = t.Format(time.RFC3339)
	}
	cl, err := a.apiClient(c, env)
	if err != nil {
		return err
	}
	var ans struct {
		Link string `json:"link"`
	}
	if err := cl.JSON(c.ctx, http.MethodPost, "/environments/"+url.PathEscape(env)+"/shares", body, &ans); err != nil {
		return err
	}
	what := "full access to"
	if body.Level == "observe" {
		what = "observe-only access to"
	}
	fmt.Fprintf(a.Stderr, "Shared %s %s as %s. This link works once: anyone with it can sign in as %s until it is used.\n", what, env, user, user)
	fmt.Fprintln(a.Stdout, ans.Link)
	return nil
}

func runShareLink(a *App, c *call) error {
	cl, err := a.apiClient(c, "")
	if err != nil {
		return err
	}
	user := c.args[0]
	return a.printLink(c, cl, "/shares/"+url.PathEscape(user)+"/link", user, "The new link of the share "+user)
}

func runShareRevoke(a *App, c *call) error {
	cl, err := a.apiClient(c, "")
	if err != nil {
		return err
	}
	if err := cl.JSON(c.ctx, http.MethodDelete, "/shares/"+url.PathEscape(c.args[0]), nil, nil); err != nil {
		return err
	}
	fmt.Fprintf(a.Stdout, "Revoked the share %s; its connections are closed.\n", c.args[0])
	return nil
}

// ---- devices

// devicePath is the credentials route of --account, or of the signed-in
// account.
func devicePath(c *call) string {
	if user := c.str("--account", ""); user != "" {
		return "/accounts/" + url.PathEscape(user) + "/credentials"
	}
	return "/me/credentials"
}

func runDeviceList(a *App, c *call) error {
	cl, err := a.apiClient(c, "")
	if err != nil {
		return err
	}
	var list []struct {
		ID       string  `json:"id"`
		Name     string  `json:"name"`
		Created  *string `json:"created"`
		LastUsed *string `json:"lastUsed"`
		Current  bool    `json:"current"`
	}
	if done, err := getJSON(a, c, cl, devicePath(c), &list); done || err != nil {
		return err
	}
	if len(list) == 0 {
		fmt.Fprintln(a.Stdout, "No signed-in devices.")
		return nil
	}
	tw := tabwriter.NewWriter(a.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "  ID\tNAME\tSIGNED IN\tLAST USED")
	for _, d := range list {
		mark := " "
		if d.Current {
			mark = "*"
		}
		fmt.Fprintf(tw, "%s %s\t%s\t%s\t%s\n", mark, d.ID, d.Name, when(d.Created), when(d.LastUsed))
	}
	return tw.Flush()
}

func runDeviceRevoke(a *App, c *call) error {
	cl, err := a.apiClient(c, "")
	if err != nil {
		return err
	}
	id := c.args[0]
	if err := cl.JSON(c.ctx, http.MethodDelete, devicePath(c)+"/"+url.PathEscape(id), nil, nil); err != nil {
		return err
	}
	fmt.Fprintf(a.Stdout, "Signed out the device %s; its connections are closed.\n", id)
	if c.str("--account", "") == "" && id == remote.CredentialID(cl.Token) {
		fmt.Fprintf(a.Stderr, "That was this CLI's own credential: run `deskyard logout %s` to forget the server, or sign in again.\n", cl.Alias)
	}
	return nil
}
