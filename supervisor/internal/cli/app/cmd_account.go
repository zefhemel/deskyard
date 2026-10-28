package app

import (
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/zefhemel/deskyard/supervisor/internal/cli/remote"
)

// The account commands.

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

// access is what an account may use, for tables: "-" for nothing.
func (ac account) access() string {
	if ac.Admin {
		return "admin (everything)"
	}
	return dash(formatGrants(ac.Grants))
}

// accountPath is the API route p ("", /link) of the account user.
func accountPath(user, p string) string { return "/accounts/" + url.PathEscape(user) + p }

func runAccountList(a *App, c *call, cl *remote.Client) error {
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
	var rows [][]string
	for _, ac := range list {
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
		rows = append(rows, []string{ac.Username, dash(ac.Kind), ac.access(), when(ac.Expires), when(ac.LastLogin), dash(strings.Join(note, "; "))})
	}
	return table(a.Stdout, []string{"USER", "KIND", "ACCESS", "EXPIRES", "LAST SIGN-IN", "NOTE"}, rows)
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

func runAccountCreate(a *App, c *call, cl *remote.Client) error {
	user := c.args[0]
	body := struct {
		Username    string  `json:"username"`
		Password    string  `json:"password,omitempty"`
		Admin       bool    `json:"admin,omitempty"`
		Description *string `json:"description,omitempty"`
		Grants      []grant `json:"grants,omitempty"`
		Expires     string  `json:"expires,omitempty"`
	}{Username: user, Admin: c.bool("--admin")}
	var err error
	if c.bool("--password-stdin") {
		if body.Password, err = readPassword(a, c); err != nil {
			return err
		}
	}
	if v := c.flags["--description"]; len(v) > 0 {
		body.Description = &v[len(v)-1]
	}
	if body.Grants, err = parseGrants(c, "--grant", c.flags["--grant"], envGrant, "full", "observe"); err != nil {
		return err
	}
	if s := c.str("--expires", ""); s != "" {
		if body.Expires, err = parseWhen(c, s); err != nil {
			return err
		}
	}
	if err := cl.JSON(c.ctx, http.MethodPost, "/accounts", body, nil); err != nil {
		return err
	}
	if body.Password != "" {
		fmt.Fprintf(a.Stdout, "Created the account %s; it signs in with its password.\n", user)
		return nil
	}
	return a.printLink(c, cl, accountPath(user, "/link"), nil, user,
		fmt.Sprintf("Created the token account %s. Its login link", user))
}

func runAccountUpdate(a *App, c *call, cl *remote.Client) error {
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
		at, err := parseWhen(c, s)
		if err != nil {
			return err
		}
		body["expires"] = at
	}
	changes, err := parseGrants(c, "--grant", c.flags["--grant"], envGrant, "full", "observe", "none")
	if err != nil {
		return err
	}
	if len(body) == 0 && len(changes) == 0 {
		return usagef(c.cmd, "nothing to change: give --grant, --admin, --no-admin, --description, --expires or --password-stdin")
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
		body["grants"] = mergeGrants(cur.Grants, changes)
	}
	var ac account
	if err := cl.JSON(c.ctx, http.MethodPatch, accountPath(user, ""), body, &ac); err != nil {
		return err
	}
	fmt.Fprintf(a.Stdout, "Updated %s: %s; expires %s.\n", user, ac.access(), when(ac.Expires))
	return nil
}

func runAccountRemove(a *App, c *call, cl *remote.Client) error {
	if err := cl.JSON(c.ctx, http.MethodDelete, accountPath(c.args[0], ""), nil, nil); err != nil {
		return err
	}
	fmt.Fprintf(a.Stdout, "Removed the account %s, with its login link, devices and the shares it made.\n", c.args[0])
	return nil
}

func runAccountLink(a *App, c *call, cl *remote.Client) error {
	user := c.args[0]
	return a.printLink(c, cl, accountPath(user, "/link"), nil, user, "The login link of "+user)
}
