// Package remote keeps the `deskyard` command's servers (servers.json) and
// talks to a supervisor's API with a client credential.
package remote

import (
	"fmt"
	"net/url"
	"strings"

	"github.com/zefhemel/deskyard/supervisor/internal/envname"
)

// Target is a parsed environment URL: the supervisor's base URL and the
// environment's name (empty when the URL names only the supervisor).
type Target struct {
	Base        string // scheme://host[:port][/prefix], no trailing slash
	Environment string
}

// ParseURL splits an environment URL such as https://host/alice or
// http://127.0.0.1:8080/prefix/alice into the supervisor base URL and the
// environment name (the last path segment). A URL without a path names only
// the supervisor. A pasted API URL (…/api/environments/alice/exec) names the
// supervisor before /api and the environment after environments/, and so
// does the web UI's page of an environment (…/environments/alice). Any other
// page of the web UI (…/accounts, …/volumes/photos) is an error.
func ParseURL(raw string) (Target, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return Target{}, fmt.Errorf("invalid URL %q: %v", raw, err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return Target{}, fmt.Errorf("invalid URL %q: it must start with https:// (or http://), like https://host/alice", raw)
	}
	if u.Host == "" {
		return Target{}, fmt.Errorf("invalid URL %q: no host, expected something like https://host/alice", raw)
	}
	if u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return Target{}, fmt.Errorf("invalid URL %q: expected just https://host/alice, without user info, query or fragment", raw)
	}
	path := strings.Trim(u.Path, "/")
	// A pasted API URL: the base ends before /api.
	if i := strings.Index("/"+path+"/", "/api/"); i >= 0 {
		api := strings.Split(strings.Trim(("/" + path + "/")[i+len("/api/"):], "/"), "/")
		t := Target{Base: u.Scheme + "://" + u.Host + prefix(strings.Trim(path[:max(i-1, 0)], "/"))}
		if len(api) >= 2 && api[0] == "environments" {
			t.Environment = api[1]
		}
		return t, nil
	}
	if path == "" {
		return Target{Base: u.Scheme + "://" + u.Host}, nil
	}
	segs := strings.Split(path, "/")
	name, base := segs[len(segs)-1], segs[:len(segs)-1]
	if len(base) > 0 && base[len(base)-1] == "environments" {
		base = base[:len(base)-1]
	}
	// Another page of the web UI (…/accounts), or one below it
	// (…/volumes/photos), names no environment.
	page := -1 // the index of the page's segment
	switch {
	case envname.IsReserved(name):
		page = len(base)
	case len(base) > 0 && envname.IsReserved(base[len(base)-1]):
		page = len(base) - 1
	}
	if page >= 0 {
		server := u.Scheme + "://" + u.Host + prefix(strings.Join(base[:page], "/"))
		return Target{}, fmt.Errorf("%s is a page of Deskyard, it isn't an environment: use the server's address %s, or add an environment's name to it", strings.TrimSpace(raw), server)
	}
	return Target{
		Base:        u.Scheme + "://" + u.Host + prefix(strings.Join(base, "/")),
		Environment: name,
	}, nil
}

func prefix(p string) string {
	if p == "" {
		return ""
	}
	return "/" + p
}

// URL is the human-facing environment URL (base + name).
func (t Target) URL() string {
	if t.Environment == "" {
		return t.Base
	}
	return t.Base + "/" + t.Environment
}

// ParseLink splits a login link such as https://host/link#dyl_… into the
// supervisor's base URL and the link's code (the part after #).
func ParseLink(raw string) (base, code string, err error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return "", "", fmt.Errorf("invalid link %q: %v", raw, err)
	}
	path := strings.TrimSuffix(u.Path, "/")
	if (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || !strings.HasSuffix(path, "/link") || !strings.HasPrefix(u.Fragment, "dyl_") {
		return "", "", fmt.Errorf("%q isn't a login link: copy the whole link, like https://host/link#dyl_…", raw)
	}
	return u.Scheme + "://" + u.Host + strings.TrimSuffix(path, "/link"), u.Fragment, nil
}
