package app

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/zefhemel/deskyard/supervisor/internal/cli/remote"
)

// The administrator commands: environments (cmd_env.go), volumes
// (cmd_volume.go), accounts (cmd_account.go), shares (cmd_share.go) and
// signed-in devices (cmd_device.go), all through the server's API
// (supervisor/API.md), and what they share (this file). The server decides
// who may do what; its refusals are printed as they come.

// adminRun is an administrator command, given the client it talks through.
type adminRun func(a *App, c *call, cl *remote.Client) error

// onServer runs a command that manages a server: the one -e SERVER/… or
// DESKYARD_ENVIRONMENT names, else the default one.
func onServer(run adminRun) func(a *App, c *call) error {
	return func(a *App, c *call) error {
		cl, err := a.apiClient()
		if err != nil {
			return err
		}
		return run(a, c, cl)
	}
}

// onEnv runs a command about one environment, its first argument
// [SERVER/]NAME (see envClient); the client's Environment is its name.
func onEnv(run adminRun) func(a *App, c *call) error {
	return func(a *App, c *call) error {
		cl, err := a.envClient(c, c.arg(0))
		if err != nil {
			return err
		}
		return run(a, c, cl)
	}
}

// timeNow is the clock --expires durations count from (tests fix it).
var timeNow = time.Now

// apiClient is the client for the server -e SERVER/… or
// DESKYARD_ENVIRONMENT names, else the default one.
func (a *App) apiClient() (*remote.Client, error) {
	sel, err := a.selector()
	if err != nil {
		return nil, err
	}
	name, _, _ := splitSelector(sel)
	srv, err := a.server(name)
	if err != nil {
		return nil, err
	}
	if srv == nil {
		return nil, a.noServer()
	}
	return a.client(srv, ""), nil
}

// envClient is the client for the environment a command is about, its
// Environment the name: arg is [SERVER/]NAME, NAME on the server -e names
// (else the default server). Without arg (when the command's NAME is
// optional) it is the environment -e or DESKYARD_ENVIRONMENT chooses, else
// the server's default environment, like the commands that act on one.
func (a *App) envClient(c *call, arg string) (*remote.Client, error) {
	sel, err := a.selector()
	if err != nil {
		return nil, err
	}
	name, env, _ := splitSelector(sel)
	if arg != "" {
		server, e, err := splitSelector(arg)
		if err != nil || e == "" {
			return nil, usagef(c.cmd, "%q isn't an environment: write NAME, or SERVER/NAME for another server", arg)
		}
		if server != "" {
			name = server
		}
		env = e
	}
	srv, err := a.server(name)
	if err != nil {
		return nil, err
	}
	if srv == nil {
		return nil, a.noServer()
	}
	if env == "" {
		env = srv.Environment
	}
	if env == "" {
		return nil, usagef(c.cmd, "no environment chosen on %s: give its NAME, use -e ENV, or choose a default with `deskyard env use ENV`", srv.Name)
	}
	return a.client(srv, env), nil
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

// parseWhen reads --expires, a duration from now (90m, 2h, 7d, 2w) or an
// RFC 3339 time, into the API's RFC 3339 UTC time.
func parseWhen(c *call, s string) (string, error) {
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t.UTC().Format(time.RFC3339), nil
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
		return "", usagef(c.cmd, "--expires wants a time from now (2h, 7d, 2w) or an RFC 3339 time (2026-12-01T18:00:00Z), got %q", s)
	}
	return timeNow().Add(d).UTC().Truncate(time.Second).Format(time.RFC3339), nil
}

// table prints rows under header as columns two spaces apart.
func table(w io.Writer, header []string, rows [][]string) error {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, strings.Join(header, "\t"))
	for _, row := range rows {
		fmt.Fprintln(tw, strings.Join(row, "\t"))
	}
	return tw.Flush()
}

// parseGrants reads NAME[=LEVEL] items, a bare NAME being full, into grants
// made by mk. allowed are the levels accepted after =; a name given twice is
// refused. what names the items in messages (--grant, USER=LEVEL).
func parseGrants(c *call, what string, items []string, mk func(name, level string) grant, allowed ...string) ([]grant, error) {
	var out []grant
	seen := map[string]bool{}
	for _, s := range items {
		name, level, ok := strings.Cut(s, "=")
		if !ok {
			level = "full"
		}
		if name == "" || !slices.Contains(allowed, level) {
			return nil, usagef(c.cmd, "%s %q: write NAME or NAME=LEVEL, with LEVEL one of %s", what, s, strings.Join(allowed, ", "))
		}
		if seen[name] {
			return nil, usagef(c.cmd, "%s: %s is listed twice", what, name)
		}
		seen[name] = true
		out = append(out, mk(name, level))
	}
	return out, nil
}

// mergeGrants is cur with changes applied, for an API that replaces the
// whole list: the grants not named are kept, then the changes are added in
// the order given, except those to none, which remove one.
func mergeGrants(cur, changes []grant) []grant {
	named := map[string]bool{}
	for _, g := range changes {
		named[g.name()] = true
	}
	out := []grant{}
	for _, g := range cur {
		if !named[g.name()] {
			out = append(out, g)
		}
	}
	for _, g := range changes {
		if g.Level != "none" {
			out = append(out, g)
		}
	}
	return out
}

// grant is a level of access: an account's in an environment's access list
// (Username), or one to an environment in an account's grants (Environment).
type grant struct {
	Username    string `json:"username,omitempty"`
	Environment string `json:"environment,omitempty"`
	Level       string `json:"level"`
}

func userGrant(user, level string) grant { return grant{Username: user, Level: level} }
func envGrant(env, level string) grant   { return grant{Environment: env, Level: level} }

// name is whom or what the grant is for.
func (g grant) name() string { return g.Username + g.Environment }

// formatGrants lists grants as "anna, bob (observe)".
func formatGrants(gs []grant) string {
	var out []string
	for _, g := range gs {
		if g.Level == "full" {
			out = append(out, g.name())
		} else {
			out = append(out, g.name()+" ("+g.Level+")")
		}
	}
	return strings.Join(out, ", ")
}

// mount is a volume granted to an environment, the same both ways round
// (an environment's volumes, a volume's environments).
type mount struct {
	Name     string `json:"name"`
	ReadOnly bool   `json:"readOnly"`
}

// formatMounts lists mounts as "photos (read-only), music", "-" for none.
func formatMounts(ms []mount) string {
	var out []string
	for _, m := range ms {
		if m.ReadOnly {
			out = append(out, m.Name+" (read-only)")
		} else {
			out = append(out, m.Name)
		}
	}
	return dash(strings.Join(out, ", "))
}

// removalTimeout is how long a removal waits for the server, which allows
// one 5 minutes (deleting files takes long); giving up sooner would report a
// network failure for a removal still going on.
const removalTimeout = 6 * time.Minute

// printLink posts body (nil for none) to path, which answers with a login
// link for user, and prints the link alone on stdout and what (what it
// gives) on stderr.
func (a *App) printLink(c *call, cl *remote.Client, path string, body any, user, what string) error {
	var ans struct {
		Link string `json:"link"`
	}
	if err := cl.JSON(c.ctx, http.MethodPost, path, body, &ans); err != nil {
		return err
	}
	fmt.Fprintf(a.Stderr, "%s works once: anyone with it can sign in as %s until it is used. A new one replaces it.\n", what, user)
	fmt.Fprintln(a.Stdout, ans.Link)
	return nil
}
