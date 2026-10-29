// Package envname knows the top-level paths a computer can't be named
// after, for the supervisor and the CLI alike.
package envname

import "strings"

// reserved are the supervisor's own top-level routes.
var reserved = map[string]bool{
	"api": true, "login": true, "logout": true, "assets": true, "static": true,
	"healthz": true, "favicon.ico": true, "robots.txt": true,
	// The UI's own pages.
	"desktops": true, "environments": true, "images": true, "accounts": true,
	"volumes": true, "link": true, "cli-login": true, "account": true,
}

// IsReserved reports whether a top-level path segment belongs to the supervisor.
func IsReserved(segment string) bool {
	return reserved[segment] || strings.HasPrefix(segment, "_")
}
