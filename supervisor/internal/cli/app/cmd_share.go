package app

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/zefhemel/deskyard/supervisor/internal/cli/remote"
)

// The share commands.

func runShareList(a *App, c *call, cl *remote.Client) error {
	env := cl.Environment
	var list []struct {
		Username string `json:"username"`
		Level    string `json:"level"`
		SharedBy struct {
			Username string `json:"username"`
		} `json:"sharedBy"`
		Expires     *string `json:"expires"`
		LastUsed    *string `json:"lastUsed"`
		LinkPending bool    `json:"linkPending"`
		Connected   bool    `json:"connected"`
	}
	if done, err := getJSON(a, c, cl, remote.EnvPath(env, "/shares"), &list); done || err != nil {
		return err
	}
	if len(list) == 0 {
		fmt.Fprintf(a.Stdout, "%s isn't shared: share it with `deskyard share create %s NAME`.\n", env, env)
		return nil
	}
	var rows [][]string
	for _, s := range list {
		var note []string
		if s.Connected {
			note = append(note, "connected")
		}
		if s.LinkPending {
			note = append(note, "unused link")
		}
		rows = append(rows, []string{s.Username, s.Level, s.SharedBy.Username, when(s.Expires), when(s.LastUsed), dash(strings.Join(note, ", "))})
	}
	return table(a.Stdout, []string{"NAME", "LEVEL", "SHARED BY", "EXPIRES", "LAST USED", "NOTE"}, rows)
}

func runShareCreate(a *App, c *call, cl *remote.Client) error {
	env, user := cl.Environment, c.args[1]
	body := struct {
		Username string `json:"username"`
		Level    string `json:"level"`
		Expires  string `json:"expires,omitempty"`
	}{Username: user, Level: "full"}
	what := "full access to"
	if c.bool("--observe") {
		body.Level, what = "observe", "observe-only access to"
	}
	if s := c.str("--expires", ""); s != "" {
		var err error
		if body.Expires, err = parseWhen(c, s); err != nil {
			return err
		}
	}
	return a.printLink(c, cl, remote.EnvPath(env, "/shares"), body, user,
		fmt.Sprintf("Shared %s %s as %s. This link", what, env, user))
}

func runShareLink(a *App, c *call, cl *remote.Client) error {
	user := c.args[0]
	return a.printLink(c, cl, "/shares/"+url.PathEscape(user)+"/link", nil, user, "The new link of the share "+user)
}

func runShareRevoke(a *App, c *call, cl *remote.Client) error {
	if err := cl.JSON(c.ctx, http.MethodDelete, "/shares/"+url.PathEscape(c.args[0]), nil, nil); err != nil {
		return err
	}
	fmt.Fprintf(a.Stdout, "Revoked the share %s; its connections are closed.\n", c.args[0])
	return nil
}
