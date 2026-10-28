package app

import (
	"fmt"
	"net/http"
	"net/url"

	"github.com/zefhemel/deskyard/supervisor/internal/cli/remote"
)

// The device commands: signed-in CLIs (client credentials).

// devicePath is the credentials route of --account, or of the signed-in
// account.
func devicePath(c *call) string {
	if user := c.str("--account", ""); user != "" {
		return accountPath(user, "/credentials")
	}
	return "/me/credentials"
}

func runDeviceList(a *App, c *call, cl *remote.Client) error {
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
	var rows [][]string
	for _, d := range list {
		mark := " "
		if d.Current {
			mark = "*"
		}
		rows = append(rows, []string{mark + " " + d.ID, d.Name, when(d.Created), when(d.LastUsed)})
	}
	return table(a.Stdout, []string{"  ID", "NAME", "SIGNED IN", "LAST USED"}, rows)
}

func runDeviceRevoke(a *App, c *call, cl *remote.Client) error {
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
