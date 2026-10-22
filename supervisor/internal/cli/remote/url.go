// Package remote keeps the `computer` command's remotes (remotes.json) and
// talks to a supervisor's agent API.
package remote

import (
	"fmt"
	"net/url"
	"strings"
)

// Target is a parsed computer URL: the supervisor's base URL and the
// computer's name (empty when the URL names only the supervisor).
type Target struct {
	Base     string // scheme://host[:port][/prefix], no trailing slash
	Computer string
}

// ParseURL splits a computer URL such as https://host/alice or
// http://127.0.0.1:8080/prefix/alice into the supervisor base URL and the
// computer name (the last path segment). A URL without a path names only
// the supervisor; the token then decides which computer it is.
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
	// A pasted agent API URL: drop the API part.
	if i := strings.Index("/"+path+"/", "/api/agent/"); i >= 0 {
		path = strings.Trim(path[:max(i-1, 0)], "/")
		return Target{Base: u.Scheme + "://" + u.Host + prefix(path), Computer: ""}, nil
	}
	if path == "" {
		return Target{Base: u.Scheme + "://" + u.Host}, nil
	}
	segs := strings.Split(path, "/")
	name := segs[len(segs)-1]
	return Target{
		Base:     u.Scheme + "://" + u.Host + prefix(strings.Join(segs[:len(segs)-1], "/")),
		Computer: name,
	}, nil
}

func prefix(p string) string {
	if p == "" {
		return ""
	}
	return "/" + p
}

// URL is the human-facing computer URL (base + name).
func (t Target) URL() string {
	if t.Computer == "" {
		return t.Base
	}
	return t.Base + "/" + t.Computer
}
